package catalogue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

type LifecycleObservation struct {
	RevisionID        string
	SkillID           string
	Commit            string
	Tree              string
	ArchiveSHA256     string
	QueryID           string
	MaterializationID string
	Event             string
	CorrelationID     string
	Source            string
}

func (s *Store) RecordLifecycle(ctx context.Context, organizationID string, observation LifecycleObservation) error {
	if s == nil || s.DB == nil || organizationID == "" {
		return fmt.Errorf("catalogue database and organization are required")
	}
	if !validLifecycleEvent(observation.Event) {
		return fmt.Errorf("unsupported lifecycle event %q", observation.Event)
	}
	if observation.RevisionID == "" || observation.SkillID == "" || observation.Commit == "" || observation.Tree == "" || observation.ArchiveSHA256 == "" || observation.MaterializationID == "" {
		return fmt.Errorf("complete materialization and immutable revision identity is required")
	}
	var skillID, commit, tree, tarDigest, zipDigest string
	err := s.DB.QueryRowContext(ctx, `SELECT sr.skill_id, sr.commit_sha, sr.tree_sha, sr.archive_sha256_tar_gz, sr.archive_sha256_zip
		FROM skill_revisions sr
		JOIN skills sk ON sk.id = sr.skill_id
		WHERE sr.id = ? AND sk.organization_id = ?`, observation.RevisionID, organizationID).Scan(&skillID, &commit, &tree, &tarDigest, &zipDigest)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("lifecycle revision is unavailable or outside the organization")
		}
		return err
	}
	if observation.SkillID != skillID || observation.Commit != commit || observation.Tree != tree || (observation.ArchiveSHA256 != tarDigest && observation.ArchiveSHA256 != zipDigest) {
		return fmt.Errorf("lifecycle identity does not match immutable revision")
	}

	var materializationDetails string
	err = s.DB.QueryRowContext(ctx, `SELECT details_json FROM audit_events
		WHERE organization_id=? AND event_type='materialisation_prepared' AND revision_id=? AND request_id=?
		ORDER BY id DESC LIMIT 1`, organizationID, observation.RevisionID, observation.MaterializationID).Scan(&materializationDetails)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("lifecycle materialization is unavailable or outside the organization")
		}
		return err
	}
	var materialization map[string]any
	if err := json.Unmarshal([]byte(materializationDetails), &materialization); err != nil {
		return fmt.Errorf("decode materialization provenance: %w", err)
	}
	if materialization["archive_sha256"] != observation.ArchiveSHA256 || materialization["query_id"] != observation.QueryID {
		return fmt.Errorf("lifecycle provenance does not match materialization")
	}

	return s.RecordAudit(ctx, organizationID, "skill_"+observation.Event, map[string]any{
		"actor_type":         "harness",
		"actor_id":           observation.Source,
		"skill_id":           skillID,
		"revision_id":        observation.RevisionID,
		"commit":             commit,
		"tree":               tree,
		"archive_sha256":     observation.ArchiveSHA256,
		"query_id":           observation.QueryID,
		"materialization_id": observation.MaterializationID,
		"correlation_id":     observation.CorrelationID,
		"lifecycle_source":   observation.Source,
	})
}

func validLifecycleEvent(event string) bool {
	switch event {
	case "activated", "deactivated", "completed", "failed":
		return true
	default:
		return false
	}
}
