package main

import (
	"testing"

	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/search"
)

func TestLegacyRoutingDocumentsFailClosedForGovernanceAndApproval(t *testing.T) {
	docs := []search.Document{
		{ID: "active", RepositoryID: "central", TrustLevel: "approved", Searchable: true},
		{ID: "deprecated", RepositoryID: "central", TrustLevel: "approved", Searchable: true, Metadata: map[string]string{governance.StateKey: string(governance.StateDeprecated)}},
		{ID: "yanked", RepositoryID: "central", TrustLevel: "approved", Searchable: true, Metadata: map[string]string{governance.StateKey: string(governance.StateYanked)}},
		{ID: "invalid", RepositoryID: "central", TrustLevel: "approved", Searchable: true, Metadata: map[string]string{governance.StateKey: "unknown"}},
		{ID: "unapproved", RepositoryID: "central", TrustLevel: "unapproved", Searchable: true},
	}

	got := legacyRoutingDocuments(docs, nil)
	if len(got) != len(docs) {
		t.Fatalf("legacy projection unexpectedly removed retained documents: got %d want %d", len(got), len(docs))
	}
	searchable := map[string]bool{}
	for _, doc := range got {
		searchable[doc.ID] = doc.Searchable
	}
	if !searchable["active"] || !searchable["deprecated"] {
		t.Fatalf("active/deprecated approved legacy documents became ineligible: %+v", searchable)
	}
	if searchable["yanked"] || searchable["invalid"] || searchable["unapproved"] {
		t.Fatalf("yanked/invalid/unapproved governance remained eligible in legacy discovery: %+v", searchable)
	}
}
