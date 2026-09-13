package main

import (
	"testing"

	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/search"
)

func TestLegacyRoutingDocumentsFailClosedForYankedGovernance(t *testing.T) {
	docs := []search.Document{
		{ID: "active", RepositoryID: "central", Searchable: true},
		{ID: "deprecated", RepositoryID: "central", Searchable: true, Metadata: map[string]string{governance.StateKey: string(governance.StateDeprecated)}},
		{ID: "yanked", RepositoryID: "central", Searchable: true, Metadata: map[string]string{governance.StateKey: string(governance.StateYanked)}},
		{ID: "invalid", RepositoryID: "central", Searchable: true, Metadata: map[string]string{governance.StateKey: "unknown"}},
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
		t.Fatalf("active/deprecated legacy documents became ineligible: %+v", searchable)
	}
	if searchable["yanked"] || searchable["invalid"] {
		t.Fatalf("yanked/invalid governance remained eligible in legacy discovery: %+v", searchable)
	}
}
