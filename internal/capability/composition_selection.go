package capability

import (
	"fmt"
	"sort"

	semver "github.com/Masterminds/semver/v3"
)

// SelectableDetailsByIdentity returns currently routable immutable revisions
// for one stable capability identity in deterministic preference order. It does
// not perform semantic search and does not widen scope. Callers must authorize
// the stable identity before invoking this method when details could otherwise
// cross a disclosure boundary.
func (s *Service) SelectableDetailsByIdentity(identityID string, request Scope) ([]Detail, error) {
	if s == nil || s.index == nil {
		return nil, fmt.Errorf("capability service is unavailable")
	}
	if identityID == "" {
		return nil, fmt.Errorf("capability identity is required")
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if err := s.RefreshGovernance(); err != nil {
		return nil, err
	}
	out := []Detail{}
	for _, doc := range s.index.Documents() {
		if doc.SkillID != identityID || !s.visible(doc, request) {
			continue
		}
		if err := s.AllowsNewSelection(doc.ID); err != nil {
			continue
		}
		detail, err := s.DescribeDetail(doc.ID, request)
		if err != nil {
			continue
		}
		out = append(out, detail)
	}
	sort.Slice(out, func(i, j int) bool {
		a, aerr := semver.StrictNewVersion(out[i].Descriptor.Version)
		b, berr := semver.StrictNewVersion(out[j].Descriptor.Version)
		switch {
		case aerr == nil && berr == nil && !a.Equal(b):
			return a.GreaterThan(b)
		case aerr == nil && berr != nil:
			return true
		case aerr != nil && berr == nil:
			return false
		default:
			return out[i].Descriptor.Provenance.RevisionID < out[j].Descriptor.Provenance.RevisionID
		}
	})
	return out, nil
}
