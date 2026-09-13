package capability

import (
	"math"
	"testing"

	"github.com/mhingston/skillet/internal/search"
)

func TestIneligibleRepositoryCannotPerturbVisibleRanking(t *testing.T) {
	visibleDocs := []search.Document{
		doc("rev-central", "demo/central/release", "central", "release", "production canary database migration release"),
		doc("rev-a", "demo/local-a/release", "local-a", "release", "repository A canary release checklist"),
	}
	policies := []SourcePolicy{
		{RepositoryID: "local-a", Scope: mustScope(t, "demo", "team", "repo-a")},
		{RepositoryID: "local-b", Scope: mustScope(t, "demo", "team", "repo-b")},
	}
	baseline, err := New(newTestIndex(t, visibleDocs), policies)
	if err != nil {
		t.Fatal(err)
	}

	withHidden := append([]search.Document{}, visibleDocs...)
	withHidden = append(withHidden,
		doc("rev-b-1", "demo/local-b/release-1", "local-b", "release", "production canary database migration release"),
		doc("rev-b-2", "demo/local-b/release-2", "local-b", "release", "production canary database migration release"),
		doc("rev-b-3", "demo/local-b/release-3", "local-b", "release", "production canary database migration release"),
	)
	expanded, err := New(newTestIndex(t, withHidden), policies)
	if err != nil {
		t.Fatal(err)
	}

	scope := mustScope(t, "demo", "team", "repo-a")
	query := "production canary database migration release"
	want, _, err := baseline.Search(query, 50, 50, 10, 60, scope, search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := expanded.Search(query, 50, 50, 10, 60, scope, search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("visible result count changed: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Capability.Provenance.RevisionID != want[i].Capability.Provenance.RevisionID {
			t.Fatalf("rank %d changed: got %s want %s", i+1, got[i].Capability.Provenance.RevisionID, want[i].Capability.Provenance.RevisionID)
		}
		if math.Abs(got[i].Ranking.Score-want[i].Ranking.Score) > 1e-12 {
			t.Fatalf("rank %d score changed: got %.16f want %.16f", i+1, got[i].Ranking.Score, want[i].Ranking.Score)
		}
	}
}
