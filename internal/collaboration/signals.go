package collaboration

import (
	"context"
	"fmt"
	"strings"
)

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
		COALESCE(SUM(CASE WHEN event_type='skill_completed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN event_type='skill_failed' THEN 1 ELSE 0 END),0)
		FROM audit_events WHERE organization_id=? AND skill_id=? AND revision_id=? AND event_type IN ('skill_completed','skill_failed')`, organizationID, capabilityID, revisionID).Scan(&out.LifecycleCompleted, &out.LifecycleFailed); err != nil {
		return EvidenceSignals{}, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN category='effective_pattern' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN category IN ('workaround_required','user_correction') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN category='improvement_suggested' THEN 1 ELSE 0 END),0)
		FROM skill_feedback WHERE organization_id=? AND skill_id=? AND revision_id=?`, organizationID, capabilityID, revisionID).Scan(&out.EffectivePatterns, &out.WorkaroundCorrections, &out.ImprovementSuggested); err != nil {
		return EvidenceSignals{}, err
	}
	return out, nil
}
