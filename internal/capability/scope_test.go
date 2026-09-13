package capability

import (
	"testing"

	"github.com/mhingston/skillet/internal/search"
)

func TestNamespaceScopedCapabilityVisibility(t *testing.T) {
	idx := newTestIndex(t, []search.Document{
		doc("rev-central", "demo/central/common", "central", "common", "Common capability"),
		doc("rev-team", "demo/team/common", "team-local", "team-common", "Team namespace capability"),
	})
	service, err := New(idx, []SourcePolicy{{RepositoryID: "team-local", Scope: mustScope(t, "demo", "team", "")}})
	if err != nil {
		t.Fatal(err)
	}

	teamA, err := service.List(mustScope(t, "demo", "team", "repo-a"), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	assertDocumentPresent(t, teamA, "rev-central")
	assertDocumentPresent(t, teamA, "rev-team")

	teamB, err := service.List(mustScope(t, "demo", "team", "repo-b"), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	assertDocumentPresent(t, teamB, "rev-team")

	other, err := service.List(mustScope(t, "demo", "other", "repo-a"), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	assertDocumentAbsent(t, other, "rev-team")

	centralOnly, err := service.List(mustScope(t, "demo", "", ""), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	assertDocumentAbsent(t, centralOnly, "rev-team")
}

func assertDocumentPresent(t *testing.T, docs []search.Document, revisionID string) {
	t.Helper()
	for _, doc := range docs {
		if doc.ID == revisionID {
			return
		}
	}
	t.Fatalf("revision %q missing from %+v", revisionID, docs)
}

func assertDocumentAbsent(t *testing.T, docs []search.Document, revisionID string) {
	t.Helper()
	for _, doc := range docs {
		if doc.ID == revisionID {
			t.Fatalf("revision %q unexpectedly visible in %+v", revisionID, docs)
		}
	}
}
