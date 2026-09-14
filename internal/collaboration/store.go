// Package collaboration owns Skillet-local human collaboration state around
// stable capability identities. It deliberately does not own capability source,
// governance, ranking, authentication, or identity discovery.
package collaboration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	MaxCommentBytes = 8 * 1024
	MaxReasonBytes  = 512
	MaxThreadItems  = 200
	MaxActivityItems = 100
)

const (
	CommentVisible   = "visible"
	CommentModerated = "moderated"
)

// Comment is untrusted user-authored data. Callers must render Body as data,
// never instructions or raw HTML. Moderated comments retain provenance and a
// digest while their original body is removed from normal disclosure.
type Comment struct {
	ID               int64
	OrganizationID   string
	CapabilityID     string
	ActorID           string
	RevisionID        string
	Body              string
	BodySHA256        string
	State             string
	CreatedAt         string
	ModeratedAt       string
	ModeratedBy       string
	ModerationReason  string
}

type Watch struct {
	OrganizationID string
	ActorID         string
	CapabilityID    string
	RevisionID      string
	AfterCommentID  int64
	CreatedAt       string
}

type Activity struct {
	Comment
	WatchRevisionID string
}

// EvidenceSignals contains only counts directly observable in existing Skillet
// persistence. Materialisation means a prepared acquisition audit event; it is
// explicitly not activation or success.
type EvidenceSignals struct {
	Available             bool
	Materialisations      int
	LifecycleCompleted    int
	LifecycleFailed       int
	EffectivePatterns     int
	WorkaroundCorrections int
	ImprovementSuggested  int
}

type Store struct{ DB *sql.DB }

// New establishes the explicit Skillet-owned collaboration persistence
// contract. CREATE IF NOT EXISTS is intentional: collaboration is an additive
// product-owned sidecar over the catalogue schema and never changes source data.
func New(ctx context.Context, db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("collaboration database is required")
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS capability_comments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			capability_id TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			revision_id TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL,
			body_sha256 TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'visible' CHECK(state IN ('visible','moderated')),
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			moderated_at TEXT NOT NULL DEFAULT '',
			moderated_by TEXT NOT NULL DEFAULT '',
			moderation_reason TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_capability_comments_thread ON capability_comments(organization_id, capability_id, id)`,
		`CREATE TABLE IF NOT EXISTS capability_watches (
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			actor_id TEXT NOT NULL,
			capability_id TEXT NOT NULL,
			revision_id TEXT NOT NULL DEFAULT '',
			after_comment_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(organization_id, actor_id, capability_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_capability_watches_actor ON capability_watches(organization_id, actor_id, capability_id)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize collaboration schema: %w", err)
		}
	}
	return &Store{DB: db}, nil
}

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
	// Present a bounded thread oldest-to-newest while selecting the newest N.
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

func (s *Store) EvidenceSignals(ctx context.Context, organizationID, capabilityID, revisionID string) (EvidenceSignals, error) {
	if strings.TrimSpace(organizationID) == "" || strings.TrimSpace(capabilityID) == "" || strings.TrimSpace(revisionID) == "" {
		return EvidenceSignals{}, fmt.Errorf("organization, capability, and revision are required")
	}
	var available int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_revisions r JOIN skills sk ON sk.id=r.skill_id WHERE sk.organization_id=? AND sk.id=? AND r.id=?`, organizationID, capabilityID, revisionID).Scan(&available); err != nil {
		return EvidenceSignals{}, err
	}
	if available == 0 {
		return EvidenceSignals{}, nil
	}
	out := EvidenceSignals{Available: true}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE organization_id=? AND skill_id=? AND revision_id=? AND event_type='materialisation_prepared'`, organizationID, capabilityID, revisionID).Scan(&out.Materialisations); err != nil {
		return EvidenceSignals{}, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT
		SUM(CASE WHEN event_type='skill_completed' THEN 1 ELSE 0 END),
		SUM(CASE WHEN event_type='skill_failed' THEN 1 ELSE 0 END)
		FROM audit_events WHERE organization_id=? AND skill_id=? AND revision_id=? AND event_type IN ('skill_completed','skill_failed')`, organizationID, capabilityID, revisionID).Scan(&out.LifecycleCompleted, &out.LifecycleFailed); err != nil {
		return EvidenceSignals{}, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT
		SUM(CASE WHEN category='effective_pattern' THEN 1 ELSE 0 END),
		SUM(CASE WHEN category IN ('workaround_required','user_correction') THEN 1 ELSE 0 END),
		SUM(CASE WHEN category='improvement_suggested' THEN 1 ELSE 0 END)
		FROM skill_feedback WHERE organization_id=? AND skill_id=? AND revision_id=?`, organizationID, capabilityID, revisionID).Scan(&out.EffectivePatterns, &out.WorkaroundCorrections, &out.ImprovementSuggested); err != nil {
		return EvidenceSignals{}, err
	}
	return out, nil
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
