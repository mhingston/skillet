package lineage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/experiment"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
)

func TestLineagePreservesCompetingHistoryWithoutChangingCanonicalState(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	tarDigest, _ := packages.Put("tar.gz", []byte("tar"))
	zipDigest, _ := packages.Put("zip", []byte("zip"))
	catalog := catalogue.New(db, packages)
	repo := catalogue.Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.invalid/skills", Ref: "main"}
	skill := discovery.Skill{RelativePath: "release", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "release", Description: "release"}}
	a, err := catalog.Admit(ctx, repo, skill, "commit-a", "tree-a", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	b, err := catalog.Admit(ctx, repo, skill, "commit-b", "tree-b", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Admit(ctx, repo, skill, "commit-c", "tree-c", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := experiment.New(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	insertExperiment(t, ctx, catalog, "exp-ab", a.SkillID, a.ID, "cand-b")
	insertExperiment(t, ctx, catalog, "exp-ac", a.SkillID, a.ID, "cand-c")
	insertExperiment(t, ctx, catalog, "exp-bc", b.SkillID, b.ID, "cand-c2")
	insertExperiment(t, ctx, catalog, "exp-ca", c.SkillID, c.ID, "cand-a")

	lineages, err := New(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	var activeBefore string
	var searchableBefore int
	if err := db.QueryRowContext(ctx, `SELECT active_revision_id, searchable FROM skills WHERE id=?`, a.SkillID).Scan(&activeBefore, &searchableBefore); err != nil {
		t.Fatal(err)
	}

	ab, err := lineages.Record(ctx, RecordInput{OrganizationID: "demo", ActorID: "reviewer", ParentRevisionID: a.ID, DescendantKind: DescendantRevision, DescendantID: b.ID, Relationship: RelationshipDerivedFrom, ExperimentIDs: []string{"exp-ab"}})
	if err != nil {
		t.Fatal(err)
	}
	ac, err := lineages.Record(ctx, RecordInput{OrganizationID: "demo", ActorID: "reviewer", ParentRevisionID: a.ID, DescendantKind: DescendantRevision, DescendantID: c.ID, Relationship: RelationshipChallengerOf, ExperimentIDs: []string{"exp-ac"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lineages.Decide(ctx, DecisionInput{OrganizationID: "demo", ActorID: "reviewer", LineageID: ac.Record.ID, State: DecisionRejected, Reference: "decision:review-42"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lineages.Record(ctx, RecordInput{OrganizationID: "demo", ActorID: "reviewer", ParentRevisionID: b.ID, DescendantKind: DescendantRevision, DescendantID: c.ID, Relationship: RelationshipDerivedFrom, ExperimentIDs: []string{"exp-bc"}}); err != nil {
		t.Fatal(err)
	}
	candidate, err := lineages.Record(ctx, RecordInput{OrganizationID: "demo", ActorID: "reviewer", ParentRevisionID: a.ID, DescendantKind: DescendantCandidate, DescendantID: "cand-b", Relationship: RelationshipChallengerOf, ExperimentIDs: []string{"exp-ab"}})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Record.DescendantKind != DescendantCandidate {
		t.Fatalf("candidate lineage kind=%q", candidate.Record.DescendantKind)
	}

	viewA, err := lineages.View(ctx, "demo", a.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(viewA.Descendants) != 3 {
		t.Fatalf("A descendants=%d, want 3: %+v", len(viewA.Descendants), viewA.Descendants)
	}
	var sawRejected bool
	for _, entry := range viewA.Descendants {
		if entry.Record.ID == ac.Record.ID && entry.Decision != nil && entry.Decision.State == DecisionRejected {
			sawRejected = true
		}
	}
	if !sawRejected {
		t.Fatal("rejected challenger disappeared from historical lineage")
	}

	viewB, err := lineages.View(ctx, "demo", b.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(viewB.Parents) != 1 || viewB.Parents[0].Record.ID != ab.Record.ID || len(viewB.Descendants) != 1 {
		t.Fatalf("linear A -> B -> C lineage not visible from B: %+v", viewB)
	}
	if _, err := lineages.Record(ctx, RecordInput{OrganizationID: "demo", ActorID: "reviewer", ParentRevisionID: c.ID, DescendantKind: DescendantRevision, DescendantID: a.ID, Relationship: RelationshipDerivedFrom, ExperimentIDs: []string{"exp-ca"}}); !errors.Is(err, ErrCycle) {
		t.Fatalf("cycle error=%v, want ErrCycle", err)
	}
	if _, err := lineages.Record(ctx, RecordInput{OrganizationID: "demo", ActorID: "reviewer", ParentRevisionID: a.ID, DescendantKind: DescendantRevision, DescendantID: b.ID, Relationship: "depends_on", ExperimentIDs: []string{"exp-ab"}}); err == nil {
		t.Fatal("malformed/semantic-overload relationship was accepted")
	}
	if _, err := lineages.View(ctx, "other", a.ID, 10); err == nil {
		t.Fatal("cross-scope lineage view leaked a revision")
	}

	var activeAfter string
	var searchableAfter int
	if err := db.QueryRowContext(ctx, `SELECT active_revision_id, searchable FROM skills WHERE id=?`, a.SkillID).Scan(&activeAfter, &searchableAfter); err != nil {
		t.Fatal(err)
	}
	if activeAfter != activeBefore || searchableAfter != searchableBefore {
		t.Fatalf("lineage changed canonical resolution/routing state: before=(%s,%d) after=(%s,%d)", activeBefore, searchableBefore, activeAfter, searchableAfter)
	}
}

func TestLineageDecisionsAreImmutable(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	tarDigest, _ := packages.Put("tar.gz", []byte("tar"))
	zipDigest, _ := packages.Put("zip", []byte("zip"))
	catalog := catalogue.New(db, packages)
	repo := catalogue.Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.invalid/skills", Ref: "main"}
	skill := discovery.Skill{RelativePath: "plan", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "plan", Description: "plan"}}
	a, _ := catalog.Admit(ctx, repo, skill, "a", "ta", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	b, _ := catalog.Admit(ctx, repo, skill, "b", "tb", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if _, err := experiment.New(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	insertExperiment(t, ctx, catalog, "exp", a.SkillID, a.ID, "cand")
	lineages, _ := New(ctx, catalog)
	entry, err := lineages.Record(ctx, RecordInput{OrganizationID: "demo", ActorID: "reviewer", ParentRevisionID: a.ID, DescendantKind: DescendantRevision, DescendantID: b.ID, Relationship: RelationshipDerivedFrom, ExperimentIDs: []string{"exp"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lineages.Decide(ctx, DecisionInput{OrganizationID: "demo", ActorID: "reviewer", LineageID: entry.Record.ID, State: DecisionRejected, Reference: "decision:one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lineages.Decide(ctx, DecisionInput{OrganizationID: "demo", ActorID: "reviewer", LineageID: entry.Record.ID, State: DecisionPromoted, Reference: "decision:two"}); err == nil {
		t.Fatal("historical decision was mutable")
	}
}

func insertExperiment(t *testing.T, ctx context.Context, catalog *catalogue.Store, id, capabilityID, baseRevisionID, candidateID string) {
	t.Helper()
	_, err := catalog.DB.ExecContext(ctx, `INSERT INTO improvement_experiments(
		id, organization_id, capability_id, base_revision_id, proposal_id, candidate_id,
		proposal_snapshot_sha256, evidence_json, hypothesis, intended_outcome, executor_json,
		eval_suite_json, budget_json, spec_revision, handoff_json, handoff_sha256, actor_id
	) VALUES (?, 'demo', ?, ?, ?, ?, ?, '[]', 'hypothesis', 'outcome', '{}', ?, '{}', ?, '{}', ?, 'runner')`,
		id, capabilityID, baseRevisionID, "proposal-"+id, candidateID, "snapshot-"+id,
		`{"id":"quality","version":"v1","protected":[]}`, "spec-"+id, "handoff-"+id)
	if err != nil {
		t.Fatal(err)
	}
}
