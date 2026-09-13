package capability

import (
	"testing"

	"github.com/mhingston/skillet/internal/search"
)

func TestScopedSearchCentralAndRepositoryLocalVisibility(t *testing.T) {
	idx := newTestIndex(t, []search.Document{
		doc("rev-central-plan", "demo/central/plan", "central", "plan", "Plan implementation work across repositories"),
		doc("rev-a-plan", "demo/local-a/plan", "local-a", "plan", "Plan repository A migrations and release work"),
		doc("rev-b-plan", "demo/local-b/plan", "local-b", "plan", "Plan repository B migrations and release work"),
		doc("rev-a-k8s", "demo/local-a/k8s", "local-a", "kubernetes", "Tune Kubernetes manifests for repository A"),
	})
	service, err := New(idx, []SourcePolicy{
		{RepositoryID: "local-a", Scope: mustScope(t, "demo", "team", "repo-a")},
		{RepositoryID: "local-b", Scope: mustScope(t, "demo", "team", "repo-b")},
	})
	if err != nil {
		t.Fatal(err)
	}

	a, _, err := service.Search("plan repository A migrations", 50, 50, 10, 60, mustScope(t, "demo", "team", "repo-a"), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a) < 2 || a[0].Capability.Provenance.RevisionID != "rev-a-plan" || a[1].Capability.Provenance.RevisionID != "rev-central-plan" {
		t.Fatalf("repository A ranking = %+v; expected A-local plan then central plan", a)
	}
	assertContains(t, a, "rev-a-k8s")
	assertNoRevision(t, a, "rev-b-plan")

	b, _, err := service.Search("plan repository B migrations", 50, 50, 10, 60, mustScope(t, "demo", "team", "repo-b"), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 2 || b[0].Capability.Provenance.RevisionID != "rev-b-plan" || b[1].Capability.Provenance.RevisionID != "rev-central-plan" {
		t.Fatalf("repository B ranking = %+v; expected B-local plan then central plan", b)
	}
	assertNoRevision(t, b, "rev-a-plan")
	assertNoRevision(t, b, "rev-a-k8s")

	centralOnly, _, err := service.Search("plan implementation work", 50, 50, 10, 60, mustScope(t, "demo", "", ""), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, centralOnly, []string{"rev-central-plan"})
	assertNoRevision(t, centralOnly, "rev-a-plan")
	assertNoRevision(t, centralOnly, "rev-b-plan")
	assertNoRevision(t, centralOnly, "rev-a-k8s")
}

func TestLocalCapabilityDoesNotBeatMoreRelevantCentralCapability(t *testing.T) {
	idx := newTestIndex(t, []search.Document{
		doc("rev-central", "demo/central/db", "central", "database-migration", "Design a PostgreSQL zero-downtime schema migration and rollback plan"),
		doc("rev-local", "demo/local-a/readme", "local-a", "migration-notes", "Update repository A README wording and documentation notes"),
	})
	service, err := New(idx, []SourcePolicy{{RepositoryID: "local-a", Scope: mustScope(t, "demo", "team", "repo-a")}})
	if err != nil {
		t.Fatal(err)
	}
	results, _, err := service.Search("PostgreSQL zero-downtime schema migration rollback", 50, 50, 10, 60, mustScope(t, "demo", "team", "repo-a"), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Capability.Provenance.RevisionID != "rev-central" {
		t.Fatalf("results = %+v; local scope must not override relevance", results)
	}
}

func TestScopeLeakageAndMalformedScopeAreRejected(t *testing.T) {
	idx := newTestIndex(t, []search.Document{
		doc("rev-central", "demo/central/common", "central", "common", "Common release workflow"),
		doc("rev-a", "demo/local-a/secret", "local-a", "repo-a-only", "Repository A private release workflow"),
		doc("rev-b", "demo/local-b/secret", "local-b", "repo-b-only", "Repository B private release workflow"),
	})
	service, err := New(idx, []SourcePolicy{
		{RepositoryID: "local-a", Scope: mustScope(t, "demo", "team", "repo-a")},
		{RepositoryID: "local-b", Scope: mustScope(t, "demo", "team", "repo-b")},
	})
	if err != nil {
		t.Fatal(err)
	}
	results, _, err := service.Search("private release workflow", 50, 50, 10, 60, mustScope(t, "demo", "team", "repo-a"), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	assertNoRevision(t, results, "rev-b")
	for _, result := range results {
		for _, neighbor := range result.Ranking.SemanticNeighbors {
			if neighbor.Skill.ID == "rev-b" {
				t.Fatalf("repository B leaked through semantic-neighbour evidence: %+v", result)
			}
		}
	}

	if _, _, err := service.Search("anything", 50, 50, 10, 60, Scope{Organization: "demo", Namespace: "team", Repository: "../repo-b"}, search.Filters{}); err == nil {
		t.Fatal("malformed repository scope was accepted")
	}
	if _, _, err := service.Search("anything", 50, 50, 10, 60, Scope{Organization: "demo", Namespace: "team", Repository: "repo-b/../repo-a"}, search.Filters{}); err == nil {
		t.Fatal("forged traversal-like scope was accepted")
	}
}

func TestMultiCapabilitySearchSpansCentralAndLocal(t *testing.T) {
	idx := newTestIndex(t, []search.Document{
		doc("rev-central-security", "demo/central/security", "central", "security-review", "Review authentication authorization and threat model"),
		doc("rev-a-deploy", "demo/local-a/deploy", "local-a", "deploy", "Deploy repository A service and verify rollout"),
		doc("rev-b-deploy", "demo/local-b/deploy", "local-b", "deploy", "Deploy repository B service and verify rollout"),
	})
	service, err := New(idx, []SourcePolicy{
		{RepositoryID: "local-a", Scope: mustScope(t, "demo", "team", "repo-a")},
		{RepositoryID: "local-b", Scope: mustScope(t, "demo", "team", "repo-b")},
	})
	if err != nil {
		t.Fatal(err)
	}
	results, _, err := service.Search("review authentication then deploy repository A service", 50, 50, 10, 60, mustScope(t, "demo", "team", "repo-a"), search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, results, "rev-central-security")
	assertContains(t, results, "rev-a-deploy")
	assertNoRevision(t, results, "rev-b-deploy")
}

func TestDescribeHonoursSameScopeBoundary(t *testing.T) {
	idx := newTestIndex(t, []search.Document{doc("rev-a", "demo/local-a/plan", "local-a", "plan", "Repository A plan")})
	service, err := New(idx, []SourcePolicy{{RepositoryID: "local-a", Scope: mustScope(t, "demo", "team", "repo-a")}})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := service.Describe("rev-a", mustScope(t, "demo", "team", "repo-a"))
	if err != nil {
		t.Fatal(err)
	}
	if detail.Identity.ID != "demo/local-a/plan" || detail.Identity.Kind != KindSkill || detail.Provenance.RevisionID != "rev-a" {
		t.Fatalf("descriptor = %+v", detail)
	}
	if _, err := service.Describe("rev-a", mustScope(t, "demo", "team", "repo-b")); err == nil {
		t.Fatal("repository-local capability was described outside its scope")
	}
}

func newTestIndex(t *testing.T, docs []search.Document) *search.Index {
	t.Helper()
	idx, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if err := idx.Add(d); err != nil {
			t.Fatal(err)
		}
	}
	return idx
}

func doc(revisionID, skillID, repositoryID, name, description string) search.Document {
	return search.Document{
		ID: revisionID, SkillID: skillID, OrganizationID: "demo", RepositoryID: repositoryID,
		Name: name, Description: description, Compatibility: "go", Commit: "commit-" + revisionID,
		Tree: "tree-" + revisionID, TrustLevel: "approved", Searchable: true,
	}
}

func mustScope(t *testing.T, organization, namespace, repository string) Scope {
	t.Helper()
	scope, err := NewScope(organization, namespace, repository)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func assertIDs(t *testing.T, results []Candidate, want []string) {
	t.Helper()
	got := make([]string, 0, len(results))
	for _, result := range results {
		got = append(got, result.Capability.Provenance.RevisionID)
	}
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

func assertContains(t *testing.T, results []Candidate, revisionID string) {
	t.Helper()
	for _, result := range results {
		if result.Capability.Provenance.RevisionID == revisionID {
			return
		}
	}
	t.Fatalf("revision %s not found in %+v", revisionID, results)
}

func assertNoRevision(t *testing.T, results []Candidate, revisionID string) {
	t.Helper()
	for _, result := range results {
		if result.Capability.Provenance.RevisionID == revisionID {
			t.Fatalf("revision %s leaked in %+v", revisionID, results)
		}
	}
}
