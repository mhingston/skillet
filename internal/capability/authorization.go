package capability

// ScopeForRevision returns the authoritative configured scope for an immutable
// routing revision. The boolean is false for revisions that are not present in
// the current capability index; retained historical revisions may legitimately
// be absent and are then treated as organisation-wide by compatibility callers.
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
