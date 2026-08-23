package catalogue

import (
	"context"
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
	if !validLifecycleEvent(observation.Event) {
		return fmt.Errorf("unsupported lifecycle event %q", observation.Event)
	}
	provenance, err := s.ValidateMaterialization(ctx, organizationID, MaterializationReference{
		RevisionID:        observation.RevisionID,
		SkillID:           observation.SkillID,
		Commit:            observation.Commit,
		Tree:              observation.Tree,
		ArchiveSHA256:     observation.ArchiveSHA256,
		MaterializationID: observation.MaterializationID,
	})
	if err != nil {
		return err
	}

	return s.RecordAudit(ctx, organizationID, "skill_"+observation.Event, map[string]any{
		"actor_type":         "harness",
		"actor_id":           observation.Source,
		"skill_id":           provenance.SkillID,
		"revision_id":        provenance.RevisionID,
		"commit":             provenance.Commit,
		"tree":               provenance.Tree,
		"archive_sha256":     provenance.ArchiveSHA256,
		"materialization_id": provenance.MaterializationID,
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
