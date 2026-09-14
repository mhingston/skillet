package collaboration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

func (s *Store) PostComment(ctx context.Context, organizationID, capabilityID, actorID, revisionID, body string) (Comment, error) {
	if err := validateIdentity(organizationID, capabilityID, actorID); err != nil {
		return Comment{}, err
	}
	if !utf8.ValidString(body) {
		return Comment{}, fmt.Errorf("comment must be valid UTF-8")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return Comment{}, fmt.Errorf("comment body is required")
	}
	if len([]byte(body)) > MaxCommentBytes {
		return Comment{}, fmt.Errorf("comment exceeds %d bytes", MaxCommentBytes)
	}
	digest := sha256.Sum256([]byte(body))
	result, err := s.DB.ExecContext(ctx, `INSERT INTO capability_comments(
		organization_id, capability_id, actor_id, revision_id, body, body_sha256
	) VALUES (?, ?, ?, ?, ?, ?)`, organizationID, capabilityID, actorID, strings.TrimSpace(revisionID), body, hex.EncodeToString(digest[:]))
	if err != nil {
		return Comment{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Comment{}, err
	}
	return s.comment(ctx, organizationID, capabilityID, id)
}

func (s *Store) Thread(ctx context.Context, organizationID, capabilityID string, limit int) ([]Comment, error) {
	if strings.TrimSpace(organizationID) == "" || strings.TrimSpace(capabilityID) == "" {
		return nil, fmt.Errorf("organization and capability are required")
	}
	if limit < 1 || limit > MaxThreadItems {
		return nil, fmt.Errorf("thread limit must be between 1 and %d", MaxThreadItems)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, organization_id, capability_id, actor_id, revision_id, body, body_sha256, state, created_at, moderated_at, moderated_by, moderation_reason
		FROM capability_comments WHERE organization_id=? AND capability_id=? ORDER BY id DESC LIMIT ?`, organizationID, capabilityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Comment, 0, limit)
	for rows.Next() {
		var item Comment
		if err := scanComment(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out, nil
}

// Moderate leaves a tombstone with actor/time/revision provenance and removes
// the body from normal disclosure. The original digest remains for audit
// correlation without retaining the removed text in the thread surface.
func (s *Store) Moderate(ctx context.Context, organizationID, capabilityID string, commentID int64, moderatorID, reason string) (Comment, bool, error) {
	if err := validateIdentity(organizationID, capabilityID, moderatorID); err != nil {
		return Comment{}, false, err
	}
	if commentID < 1 {
		return Comment{}, false, fmt.Errorf("comment id must be positive")
	}
	reason = strings.TrimSpace(reason)
	if len([]byte(reason)) > MaxReasonBytes {
		return Comment{}, false, fmt.Errorf("moderation reason exceeds %d bytes", MaxReasonBytes)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Comment{}, false, err
	}
	defer tx.Rollback()
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM capability_comments WHERE id=? AND organization_id=? AND capability_id=?`, commentID, organizationID, capabilityID).Scan(&state); err != nil {
		return Comment{}, false, err
	}
	changed := state != CommentModerated
	if changed {
		if _, err := tx.ExecContext(ctx, `UPDATE capability_comments SET body='', state='moderated', moderated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now'), moderated_by=?, moderation_reason=?
			WHERE id=? AND organization_id=? AND capability_id=?`, moderatorID, reason, commentID, organizationID, capabilityID); err != nil {
			return Comment{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Comment{}, false, err
	}
	item, err := s.comment(ctx, organizationID, capabilityID, commentID)
	return item, changed, err
}

func (s *Store) comment(ctx context.Context, organizationID, capabilityID string, id int64) (Comment, error) {
	var item Comment
	err := s.DB.QueryRowContext(ctx, `SELECT id, organization_id, capability_id, actor_id, revision_id, body, body_sha256, state, created_at, moderated_at, moderated_by, moderation_reason
		FROM capability_comments WHERE id=? AND organization_id=? AND capability_id=?`, id, organizationID, capabilityID).Scan(
		&item.ID, &item.OrganizationID, &item.CapabilityID, &item.ActorID, &item.RevisionID, &item.Body, &item.BodySHA256, &item.State, &item.CreatedAt, &item.ModeratedAt, &item.ModeratedBy, &item.ModerationReason,
	)
	return item, err
}

type scanner interface{ Scan(...any) error }

func scanComment(row scanner, item *Comment) error {
	return row.Scan(&item.ID, &item.OrganizationID, &item.CapabilityID, &item.ActorID, &item.RevisionID, &item.Body, &item.BodySHA256, &item.State, &item.CreatedAt, &item.ModeratedAt, &item.ModeratedBy, &item.ModerationReason)
}

func validateIdentity(organizationID, capabilityID, actorID string) error {
	if strings.TrimSpace(organizationID) == "" || strings.TrimSpace(capabilityID) == "" || strings.TrimSpace(actorID) == "" {
		return fmt.Errorf("organization, capability, and actor are required")
	}
	if len(organizationID) > 256 || len(capabilityID) > 1024 || len(actorID) > 512 {
		return fmt.Errorf("collaboration identity exceeds limit")
	}
	return nil
}
