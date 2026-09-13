package evidence

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
)

func TestDeriveDeduplicatesOnlyCorrelatedEquivalentEvidenceAndKeepsContradictions(t *testing.T) {
	provenance := testProvenance()
	observations := []Observation{
		testObservation(1, "feedback", "workaround_required", "run-1", "The command needed an extra flag."),
		testObservation(2, "feedback", "workaround_required", "run-1", "  the COMMAND needed an extra   flag.  "),
		testObservation(3, "feedback", "workaround_required", "run-2", "The command needed an extra flag."),
		testObservation(4, "feedback", "effective_pattern", "run-3", "The rollback check prevented stale output."),
		testObservation(5, "lifecycle", "failed", "run-4", ""),
		testObservation(6, "lifecycle", "completed", "run-5", ""),
	}

	candidates, duplicates, err := Derive(provenance, observations)
	if err != nil {
		t.Fatal(err)
	}
	if duplicates != 1 {
		t.Fatalf("duplicates ignored = %d, want 1", duplicates)
	}
	if len(candidates) != 3 {
		t.Fatalf("candidates = %+v, want workaround, effective pattern, and lifecycle failure", candidates)
	}
	byCategory := map[string]Candidate{}
	for _, candidate := range candidates {
		byCategory[candidate.Category] = candidate
		if !candidate.ContradictoryEvidencePresent {
			t.Fatalf("candidate %+v did not preserve contradictory positive/friction evidence", candidate)
		}
		if candidate.Handoff.Kind != "github_issue_draft" || candidate.Handoff.SchemaVersion != "skillet.improvement-handoff/v1" {
			t.Fatalf("handoff = %+v", candidate.Handoff)
		}
	}
	if got := byCategory["workaround_required"].SignalCount; got != 2 {
		t.Fatalf("workaround signal count = %d, want 2 distinct correlated runs", got)
	}
	if got := byCategory["effective_reusable_pattern"].SignalCount; got != 1 {
		t.Fatalf("effective pattern signal count = %d, want 1", got)
	}
	if got := byCategory["lifecycle_failure"].SignalCount; got != 1 {
		t.Fatalf("lifecycle failure signal count = %d, want 1", got)
	}
}

func TestDerivePreservesUncorrelatedRepeatedObservations(t *testing.T) {
	observations := []Observation{
		testObservation(1, "feedback", "ambiguous_instruction", "", "The step was unclear."),
		testObservation(2, "feedback", "ambiguous_instruction", "", "The step was unclear."),
	}
	candidates, duplicates, err := Derive(testProvenance(), observations)
	if err != nil {
		t.Fatal(err)
	}
	if duplicates != 0 || len(candidates) != 1 || candidates[0].SignalCount != 2 || len(candidates[0].Evidence) != 2 {
		t.Fatalf("uncorrelated evidence was over-deduplicated: candidates=%+v duplicates=%d", candidates, duplicates)
	}
}

func TestDeriveIsOrderIndependent(t *testing.T) {
	observations := []Observation{
		testObservation(11, "feedback", "user_correction", "run-2", "Use the repository-local config instead."),
		testObservation(9, "feedback", "compatibility_mismatch", "run-1", "This flag is not supported on Windows."),
		testObservation(13, "feedback", "effective_pattern", "run-3", "Verify the digest before activation."),
		testObservation(12, "lifecycle", "failed", "run-4", ""),
	}
	forward, _, err := Derive(testProvenance(), observations)
	if err != nil {
		t.Fatal(err)
	}
	reversedInput := append([]Observation(nil), observations...)
	for i, j := 0, len(reversedInput)-1; i < j; i, j = i+1, j-1 {
		reversedInput[i], reversedInput[j] = reversedInput[j], reversedInput[i]
	}
	reversed, _, err := Derive(testProvenance(), reversedInput)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(forward, reversed) {
		t.Fatalf("derivation changed with input order:\nforward=%+v\nreversed=%+v", forward, reversed)
	}
}

func TestDeriveRejectsForgedRevisionEvidence(t *testing.T) {
	forged := testObservation(1, "feedback", "step_failed", "run-1", "failed")
	forged.ArchiveSHA256 = "forged"
	if _, _, err := Derive(testProvenance(), []Observation{forged}); err == nil {
		t.Fatal("forged package provenance was accepted")
	}
	forged = testObservation(1, "feedback", "step_failed", "run-1", "failed")
	forged.StableCapabilityID = "other/skills/plan"
	if _, _, err := Derive(testProvenance(), []Observation{forged}); err == nil {
		t.Fatal("forged stable capability identity was accepted")
	}
}

func TestDeriveGoldenCandidateOutput(t *testing.T) {
	observations := []Observation{
		testObservation(7, "feedback", "effective_pattern", "run-7", "Verify the archive digest before activation."),
	}
	observations[0].Source = "fixture"
	observations[0].OccurredAt = "2026-09-13T12:00:00Z"
	candidates, duplicates, err := Derive(testProvenance(), observations)
	if err != nil {
		t.Fatal(err)
	}
	if duplicates != 0 {
		t.Fatalf("duplicates = %d", duplicates)
	}
	got, err := json.MarshalIndent(candidates, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "candidates.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+"\n" != string(want) {
		t.Fatalf("candidate golden changed:\n%s", got)
	}
}

func TestServiceReadsRevisionBoundEvidenceWithoutMutationOrCrossOrgLeakage(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	tarDigest, err := packages.Put("tar.gz", []byte("tar"))
	if err != nil {
		t.Fatal(err)
	}
	zipDigest, err := packages.Put("zip", []byte("zip"))
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogue.New(db, packages)
	skill := discovery.Skill{RelativePath: "plan", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "plan", Description: "plan"}}
	revision, err := catalog.Admit(ctx, catalogue.Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.com", Ref: "main"}, skill, "commit1", "tree1", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.RecordAudit(ctx, "demo", "materialisation_prepared", map[string]any{
		"skill_id": revision.SkillID, "revision_id": revision.ID, "archive_sha256": tarDigest, "request_id": "materialize-1",
	}); err != nil {
		t.Fatal(err)
	}
	ref := catalogue.MaterializationReference{RevisionID: revision.ID, SkillID: revision.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: tarDigest, MaterializationID: "materialize-1"}
	for _, observation := range []catalogue.FeedbackObservation{
		{Reference: ref, Category: "workaround_required", Summary: "An extra flag was required.", CorrelationID: "run-1", Source: "fixture"},
		{Reference: ref, Category: "workaround_required", Summary: "An extra flag was required.", CorrelationID: "run-1", Source: "fixture-copy"},
		{Reference: ref, Category: "effective_pattern", Summary: "Digest verification prevented stale activation.", CorrelationID: "run-2", Source: "fixture"},
	} {
		if _, err := catalog.RecordFeedback(ctx, "demo", observation); err != nil {
			t.Fatal(err)
		}
	}
	if err := catalog.RecordLifecycle(ctx, "demo", catalogue.LifecycleObservation{RevisionID: revision.ID, SkillID: revision.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: tarDigest, MaterializationID: "materialize-1", Event: "failed", CorrelationID: "run-3", Source: "fixture"}); err != nil {
		t.Fatal(err)
	}

	var auditsBefore, feedbackBefore int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&auditsBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_feedback`).Scan(&feedbackBefore); err != nil {
		t.Fatal(err)
	}
	result, err := New(catalog).Candidates(ctx, "demo", Query{RevisionID: revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 3 || result.DuplicatesIgnored != 1 || result.FeedbackEvidenceIncluded != 3 || result.LifecycleEvidenceIncluded != 1 || result.SourceEvidenceTruncated {
		t.Fatalf("result = %+v", result)
	}
	for _, candidate := range result.Candidates {
		if candidate.Provenance.RevisionID != revision.ID || candidate.Provenance.StableCapabilityID != revision.SkillID || candidate.Provenance.Commit != "commit1" || candidate.Provenance.Tree != "tree1" {
			t.Fatalf("candidate lost immutable provenance: %+v", candidate)
		}
	}
	var auditsAfter, feedbackAfter int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&auditsAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_feedback`).Scan(&feedbackAfter); err != nil {
		t.Fatal(err)
	}
	if auditsAfter != auditsBefore || feedbackAfter != feedbackBefore {
		t.Fatalf("read-only derivation mutated evidence: audit %d->%d feedback %d->%d", auditsBefore, auditsAfter, feedbackBefore, feedbackAfter)
	}
	if _, err := New(catalog).Candidates(ctx, "other", Query{RevisionID: revision.ID}); err == nil {
		t.Fatal("cross-organization revision evidence was exposed")
	}
}

func testProvenance() RevisionProvenance {
	return RevisionProvenance{StableCapabilityID: "demo/skills/plan", RevisionID: "rev_fixture", Commit: "commit1", Tree: "tree1", ArchiveSHA256TarGZ: "tar-digest", ArchiveSHA256ZIP: "zip-digest"}
}

func testObservation(id int64, kind, signal, correlationID, summary string) Observation {
	return Observation{ID: id, Kind: kind, StableCapabilityID: "demo/skills/plan", RevisionID: "rev_fixture", Commit: "commit1", Tree: "tree1", ArchiveSHA256: "tar-digest", MaterializationID: "materialize-1", Signal: signal, Summary: summary, CorrelationID: correlationID, Source: "test", OccurredAt: "2026-09-13T12:00:00Z"}
}
