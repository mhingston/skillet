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
	OrganizationID string
	SkillID        string
	TrustLevel     string
	Status         governance.State
	Metadata       governance.Metadata
}

func (s *Store) RevisionGovernance(ctx context.Context, organizationID, revisionID string) (RevisionGovernance, error) {
	if s == nil || s.DB == nil {
		return RevisionGovernance{}, fmt.Errorf("catalogue is unavailable")
	}
	if organizationID == "" || revisionID == "" {
		return RevisionGovernance{}, fmt.Errorf("organization and revision ID are required")
	}

	var record RevisionGovernance
	var metadataJSON string
	err := s.DB.QueryRowContext(ctx, `SELECT sk.organization_id, r.skill_id, repo.trust_level, r.metadata_json
		FROM skill_revisions r
		JOIN skills sk ON sk.id=r.skill_id
		JOIN repositories repo ON repo.id=sk.repository_id
		WHERE r.id=? AND sk.organization_id=? AND r.state IN ('active','superseded','removed_from_source')`, revisionID, organizationID).
		Scan(&record.OrganizationID, &record.SkillID, &record.TrustLevel, &metadataJSON)
	if err != nil {
		return RevisionGovernance{}, err
	}
	if err := parseRevisionGovernance(revisionID, metadataJSON, &record); err != nil {
		return RevisionGovernance{}, err
	}
	return record, nil
}

// ResolvedRevisionGovernance revalidates governance for an immutable revision
// that has already crossed an organization-scoped selection boundary (for
// example Catalogue.Revision or ResolveVersion). Revision IDs themselves are
// organization-bound hashes, but this method is deliberately not an
// authorization primitive: callers must authorize/resolve the revision first.
func (s *Store) ResolvedRevisionGovernance(ctx context.Context, revisionID string) (RevisionGovernance, error) {
	if s == nil || s.DB == nil {
		return RevisionGovernance{}, fmt.Errorf("catalogue is unavailable")
	}
	if revisionID == "" {
		return RevisionGovernance{}, fmt.Errorf("revision ID is required")
	}

	var record RevisionGovernance
	var metadataJSON string
	err := s.DB.QueryRowContext(ctx, `SELECT sk.organization_id, r.skill_id, repo.trust_level, r.metadata_json
		FROM skill_revisions r
		JOIN skills sk ON sk.id=r.skill_id
		JOIN repositories repo ON repo.id=sk.repository_id
		WHERE r.id=? AND r.state IN ('active','superseded','removed_from_source')`, revisionID).
		Scan(&record.OrganizationID, &record.SkillID, &record.TrustLevel, &metadataJSON)
	if err != nil {
		return RevisionGovernance{}, err
	}
	if err := parseRevisionGovernance(revisionID, metadataJSON, &record); err != nil {
		return RevisionGovernance{}, err
	}
	return record, nil
}

func parseRevisionGovernance(revisionID, metadataJSON string, record *RevisionGovernance) error {
	values := map[string]string{}
	if metadataJSON != "" {
		if err := json.Unmarshal([]byte(metadataJSON), &values); err != nil {
			return fmt.Errorf("decode governance metadata for revision %q: %w", revisionID, err)
		}
	}
	state, metadata, err := governance.Parse(values, governance.Defaults{})
	if err != nil {
		return fmt.Errorf("revision %q governance: %w", revisionID, err)
	}
	record.Status = state
	record.Metadata = metadata
	return nil
}
