package catalogue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mhingston/skillet/internal/governance"
)

// RevisionGovernance projects source-controlled governance for one immutable
// retained revision. It is intentionally separate from the active routing
// index so explicit historical version selection can be revalidated without
// treating every retained revision as a discovery candidate.
type RevisionGovernance struct {
	SkillID    string
	TrustLevel string
	Status     governance.State
	Metadata   governance.Metadata
}

func (s *Store) RevisionGovernance(ctx context.Context, organizationID, revisionID string) (RevisionGovernance, error) {
	if s == nil || s.DB == nil {
		return RevisionGovernance{}, fmt.Errorf("catalogue is unavailable")
	}
	if organizationID == "" || revisionID == "" {
		return RevisionGovernance{}, fmt.Errorf("organization and revision ID are required")
	}

	var skillID, trustLevel, metadataJSON string
	err := s.DB.QueryRowContext(ctx, `SELECT r.skill_id, repo.trust_level, r.metadata_json
		FROM skill_revisions r
		JOIN skills sk ON sk.id=r.skill_id
		JOIN repositories repo ON repo.id=sk.repository_id
		WHERE r.id=? AND sk.organization_id=? AND r.state IN ('active','superseded','removed_from_source')`, revisionID, organizationID).
		Scan(&skillID, &trustLevel, &metadataJSON)
	if err != nil {
		return RevisionGovernance{}, err
	}

	values := map[string]string{}
	if metadataJSON != "" {
		if err := json.Unmarshal([]byte(metadataJSON), &values); err != nil {
			return RevisionGovernance{}, fmt.Errorf("decode governance metadata for revision %q: %w", revisionID, err)
		}
	}
	state, metadata, err := governance.Parse(values, governance.Defaults{})
	if err != nil {
		return RevisionGovernance{}, fmt.Errorf("revision %q governance: %w", revisionID, err)
	}
	return RevisionGovernance{SkillID: skillID, TrustLevel: trustLevel, Status: state, Metadata: metadata}, nil
}
