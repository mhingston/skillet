package capability

import "strings"

// ScopeForRevision returns the authoritative configured scope for an immutable
// routing revision. The boolean is false for revisions that are not present in
// the current capability index; retained historical revisions may legitimately
// be absent and can instead be resolved from their source repository policy.
func (s *Service) ScopeForRevision(revisionID string) (Scope, bool) {
	if s == nil || s.index == nil {
		return Scope{}, false
	}
	doc, ok := s.index.Document(revisionID)
	if !ok {
		return Scope{}, false
	}
	return s.ScopeForDocument(doc), true
}

// ScopeForIdentity resolves the authoritative current scope for one stable
// capability identity. If duplicate routing documents disagree on scope it
// fails closed rather than selecting one arbitrarily.
func (s *Service) ScopeForIdentity(identityID string) (Scope, bool) {
	if s == nil || s.index == nil || strings.TrimSpace(identityID) == "" {
		return Scope{}, false
	}
	var resolved Scope
	found := false
	for _, doc := range s.index.Documents() {
		if doc.SkillID != identityID {
			continue
		}
		scope := s.ScopeForDocument(doc)
		if !found {
			resolved = scope
			found = true
			continue
		}
		if scope != resolved {
			return Scope{}, false
		}
	}
	return resolved, found
}

// ScopeForRepository returns the configured scope for a source repository even
// when the requested immutable revision is retained history and no longer lives
// in the current routing index. Catalogue repository IDs may be stored as
// organization/repository; source policies use the configured repository ID.
func (s *Service) ScopeForRepository(organization, repositoryID string) (Scope, bool) {
	if s == nil {
		return Scope{}, false
	}
	key := strings.TrimPrefix(repositoryID, organization+"/")
	policy, ok := s.policies[key]
	if !ok {
		policy, ok = s.policies[repositoryID]
	}
	if !ok {
		return Scope{}, false
	}
	return policy.Scope, true
}
