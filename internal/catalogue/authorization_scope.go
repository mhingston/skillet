package catalogue

import (
	"context"
	"fmt"
)

// RepositoryIDForSkill resolves the authoritative source repository for a
// stable skill identity without following an active revision. This remains
// valid for retained history after a skill is removed from active routing.
func (s *Store) RepositoryIDForSkill(ctx context.Context, organizationID, skillID string) (string, error) {
	if s == nil || s.DB == nil || organizationID == "" || skillID == "" {
		return "", fmt.Errorf("catalogue, organization, and skill identity are required")
	}
	var repositoryID string
	if err := s.DB.QueryRowContext(ctx, `SELECT repository_id FROM skills WHERE organization_id=? AND id=?`, organizationID, skillID).Scan(&repositoryID); err != nil {
		return "", err
	}
	return repositoryID, nil
}
