package capability

import "fmt"

// HasRevision reports whether a revision is represented in the current
// capability routing index. Retained historical revisions can exist in the
// catalogue without being discovery candidates, so callers can use this to
// choose the correct governance authority without widening discovery.
func (s *Service) HasRevision(revisionID string) bool {
	if s == nil || revisionID == "" {
		return false
	}
	_, ok := s.index.Document(revisionID)
	return ok
}

// AllowsNewSelection revalidates the current governance state immediately
// before ordinary materialization. It deliberately does not participate in
// exact lock restoration, which resolves immutable catalogue/package state
// through the restore subsystem for reproducibility.
func (s *Service) AllowsNewSelection(revisionID string) error {
	if s == nil || revisionID == "" {
		return fmt.Errorf("capability revision is required")
	}
	if err := s.RefreshGovernance(); err != nil {
		return err
	}
	doc, ok := s.index.Document(revisionID)
	if !ok || !doc.Searchable {
		return fmt.Errorf("capability revision is unavailable for new selection")
	}
	// Legacy/fallback in-memory documents can predate explicit trust metadata.
	// Preserve that compatibility while still rejecting an explicit non-approved
	// trust classification at the new-selection boundary.
	if doc.TrustLevel != "" && doc.TrustLevel != "approved" {
		return fmt.Errorf("capability revision is not approved for new selection")
	}
	if s.descriptorForDocument(doc).Status == StatusYanked {
		return fmt.Errorf("yanked capability revision is unavailable for new selection")
	}
	return nil
}
