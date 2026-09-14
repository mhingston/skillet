// Package collaboration owns Skillet-local human collaboration state around
// stable capability identities. It deliberately does not own capability source,
// governance, ranking, authentication, or identity discovery.
package collaboration

import (
	"context"
	"database/sql"
	"fmt"
)

const (
	MaxCommentBytes  = 8 * 1024
	MaxReasonBytes   = 512
	MaxThreadItems   = 200
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
	ID              int64
	OrganizationID  string
	CapabilityID    string
	ActorID          string
	RevisionID       string
	Body             string
	BodySHA256       string
	State            string
	CreatedAt        string
	ModeratedAt      string
	ModeratedBy      string
	ModerationReason string
}

type Watch struct {
	OrganizationID string
	ActorID        string
	CapabilityID   string
	RevisionID     string
	AfterCommentID int64
	CreatedAt      string
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
