package collaboration

import (
	"context"
	"fmt"
	"strings"
)

// SetWatch is idempotent. A new watch records the current maximum comment ID so
// activity contains only subsequent collaboration events rather than historical
// thread content.
func (s *Store) SetWatch(ctx context.Context, organizationID, capabilityID, actorID, revisionID string, watching bool) (bool, error) {
	if err := validateIdentity(organizationID, capabilityID, actorID); err != nil {
		return false, err
	}
	if !watching {
		result, err := s.DB.ExecContext(ctx, `DELETE FROM capability_watches WHERE organization_id=? AND actor_id=? AND capability_id=?`, organizationID, actorID, capabilityID)
		if err != nil {
			return false, err
		}
		count, err := result.RowsAffected()
		return count > 0, err
	}
	var existing int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM capability_watches WHERE organization_id=? AND actor_id=? AND capability_id=?`, organizationID, actorID, capabilityID).Scan(&existing); err != nil {
		return false, err
	}
	if existing > 0 {
		_, err := s.DB.ExecContext(ctx, `UPDATE capability_watches SET revision_id=? WHERE organization_id=? AND actor_id=? AND capability_id=?`, strings.TrimSpace(revisionID), organizationID, actorID, capabilityID)
		return false, err
	}
	var after int64
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM capability_comments WHERE organization_id=? AND capability_id=?`, organizationID, capabilityID).Scan(&after); err != nil {
		return false, err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO capability_watches(organization_id, actor_id, capability_id, revision_id, after_comment_id) VALUES (?, ?, ?, ?, ?)`, organizationID, actorID, capabilityID, strings.TrimSpace(revisionID), after)
	return err == nil, err
}

func (s *Store) IsWatching(ctx context.Context, organizationID, capabilityID, actorID string) (bool, error) {
	if err := validateIdentity(organizationID, capabilityID, actorID); err != nil {
		return false, err
	}
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM capability_watches WHERE organization_id=? AND actor_id=? AND capability_id=?`, organizationID, actorID, capabilityID).Scan(&count)
	return count > 0, err
}

func (s *Store) Activity(ctx context.Context, organizationID, actorID string, limit int) ([]Activity, error) {
	if strings.TrimSpace(organizationID) == "" || strings.TrimSpace(actorID) == "" {
		return nil, fmt.Errorf("organization and actor are required")
	}
	if limit < 1 || limit > MaxActivityItems {
		return nil, fmt.Errorf("activity limit must be between 1 and %d", MaxActivityItems)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id, c.organization_id, c.capability_id, c.actor_id, c.revision_id, c.body, c.body_sha256, c.state, c.created_at, c.moderated_at, c.moderated_by, c.moderation_reason, w.revision_id
		FROM capability_watches w JOIN capability_comments c
		ON c.organization_id=w.organization_id AND c.capability_id=w.capability_id
		WHERE w.organization_id=? AND w.actor_id=? AND c.id>w.after_comment_id
		ORDER BY c.id DESC LIMIT ?`, organizationID, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Activity, 0, limit)
	for rows.Next() {
		var item Activity
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.CapabilityID, &item.ActorID, &item.RevisionID, &item.Body, &item.BodySHA256, &item.State, &item.CreatedAt, &item.ModeratedAt, &item.ModeratedBy, &item.ModerationReason, &item.WatchRevisionID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
