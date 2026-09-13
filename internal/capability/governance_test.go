package capability

import (
	"testing"

	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/search"
)

func TestDeprecatedCapabilityRemainsDiscoverableWithExplicitSuccessorGuidance(t *testing.T) {
	old := doc("rev-old", "demo/central/old", "central", "old-release", "Legacy release procedure")
	old.Metadata = map[string]string{
		governance.DeprecatedKey: "true",
		governance.ReplacedByKey: "demo/central/new",
	}
	newer := doc("rev-new", "demo/central/new", "central", "new-release", "Replacement release procedure")

	idx := newTestIndex(t, []search.Document{old, newer})
	service, err := New(idx, []SourcePolicy{{RepositoryID: "central", Scope: mustScope(t, "demo", "", ""), Owner: "platform"}})
	if err != nil {
		t.Fatal(err)
	}
	results, _, err := service.Search("Legacy release procedure", 50, 50, 10, 60, mustScope(t, "demo", "", ""), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	var selected Descriptor
	for _, result := range results {
		if result.Capability.Identity.ID == "demo/central/old" {
			selected = result.Capability
			break
		}
	}
	if selected.Identity.ID == "" {
		t.Fatalf("deprecated capability missing from results: %+v", results)
	}
	if selected.Status != StatusDeprecated || selected.Governance.ReplacedBy != "demo/central/new" || !selected.Governance.ReplacementResolved {
		t.Fatalf("deprecated governance = %+v status=%q", selected.Governance, selected.Status)
	}
	if selected.Governance.Owner != "platform" || selected.Governance.Visibility != governance.VisibilityOrganization {
		t.Fatalf("ownership/visibility = %+v", selected.Governance)
	}
	if _, leaked := selected.Metadata[governance.ReplacedByKey]; leaked {
		t.Fatalf("control metadata leaked into routing metadata: %+v", selected.Metadata)
	}

	described, err := service.Describe("rev-old", mustScope(t, "demo", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if described.Identity.ID != "demo/central/old" || described.Provenance.RevisionID != "rev-old" {
		t.Fatalf("successor guidance silently substituted selection: %+v", described)
	}
}

func TestYankedCapabilityIsUnavailableForNewDiscoveryAndDescribe(t *testing.T) {
	yanked := doc("rev-yanked", "demo/central/yanked", "central", "unsafe-release", "Old unsafe release path")
	yanked.Metadata = map[string]string{governance.StateKey: string(StatusYanked), governance.ReasonKey: "withdrawn by maintainer"}
	active := doc("rev-active", "demo/central/active", "central", "safe-release", "Supported safe release path")
	idx := newTestIndex(t, []search.Document{yanked, active})
	service, err := New(idx, []SourcePolicy{{RepositoryID: "central", Scope: mustScope(t, "demo", "", "")}})
	if err != nil {
		t.Fatal(err)
	}
	results, _, err := service.Search("release path", 50, 50, 10, 60, mustScope(t, "demo", "", ""), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	assertNoRevision(t, results, "rev-yanked")
	assertContains(t, results, "rev-active")
	if _, err := service.Describe("rev-yanked", mustScope(t, "demo", "", "")); err == nil {
		t.Fatal("yanked capability remained available for new describe")
	}
	retained, ok := idx.Document("rev-yanked")
	if !ok || retained.SkillID != "demo/central/yanked" {
		t.Fatalf("yank deleted immutable routing identity: %+v ok=%t", retained, ok)
	}
}

func TestUnresolvedReplacementIsGuidanceNotSelectionFailure(t *testing.T) {
	old := doc("rev-old", "demo/central/old", "central", "old-release", "Legacy release procedure")
	old.Metadata = map[string]string{
		governance.DeprecatedKey: "true",
		governance.ReplacedByKey: "demo/central/not-yet-synchronised",
	}
	service, err := New(newTestIndex(t, []search.Document{old}), nil)
	if err != nil {
		t.Fatal(err)
	}
	described, err := service.Describe("rev-old", mustScope(t, "demo", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if described.Status != StatusDeprecated || described.Governance.ReplacedBy == "" || described.Governance.ReplacementResolved {
		t.Fatalf("unresolved guidance = status=%q governance=%+v", described.Status, described.Governance)
	}
	if described.Identity.ID != "demo/central/old" {
		t.Fatalf("unresolved replacement changed selected identity: %+v", described)
	}
}

func TestReplacementCannotEscapeOrganizationOrVisibility(t *testing.T) {
	crossOrg := doc("rev-old", "demo/central/old", "central", "old", "old")
	crossOrg.Metadata = map[string]string{
		governance.DeprecatedKey: "true",
		governance.ReplacedByKey: "other/repository/new",
	}
	service, err := New(newTestIndex(t, []search.Document{crossOrg}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshGovernance(); err == nil {
		t.Fatal("cross-organization replacement unexpectedly accepted")
	}

	central := doc("rev-central", "demo/central/old", "central", "old", "old")
	central.Metadata = map[string]string{
		governance.DeprecatedKey: "true",
		governance.ReplacedByKey: "demo/local-a/new",
	}
	local := doc("rev-local", "demo/local-a/new", "local-a", "new", "new")
	idx := newTestIndex(t, []search.Document{central, local})
	service, err = New(idx, []SourcePolicy{{RepositoryID: "local-a", Scope: mustScope(t, "demo", "team", "repo-a")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshGovernance(); err == nil {
		t.Fatal("organization-wide capability accepted repository-local replacement")
	}
}

func TestOwnershipChangesDoNotChangeSemanticRanking(t *testing.T) {
	docs := []search.Document{
		doc("rev-a", "demo/central/a", "central", "database-migration", "PostgreSQL zero downtime schema migration rollback"),
		doc("rev-b", "demo/central/b", "central", "readme", "Documentation wording and notes"),
	}
	searchWithOwner := func(owner string) []Candidate {
		t.Helper()
		service, err := New(newTestIndex(t, docs), []SourcePolicy{{RepositoryID: "central", Scope: mustScope(t, "demo", "", ""), Owner: owner}})
		if err != nil {
			t.Fatal(err)
		}
		results, _, err := service.Search("PostgreSQL zero downtime schema migration rollback", 50, 50, 10, 60, mustScope(t, "demo", "", ""), search.Filters{})
		if err != nil {
			t.Fatal(err)
		}
		return results
	}
	first := searchWithOwner("platform-team")
	second := searchWithOwner("completely-different-owner")
	if len(first) != len(second) {
		t.Fatalf("ranking size changed with owner: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Capability.Provenance.RevisionID != second[i].Capability.Provenance.RevisionID || first[i].Ranking.Rank != second[i].Ranking.Rank {
			t.Fatalf("ownership changed rank at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
}
