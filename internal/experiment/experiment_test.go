package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/evidence"
	"github.com/mhingston/skillet/internal/packagebuilder"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/proposal"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
)

func TestExperimentSpecIsDeterministicImmutableAndSecretFree(t *testing.T) {
	ctx := context.Background()
	catalog, info, ready := experimentFixture(t, ctx)
	experiments, err := New(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	input := CreateInput{
		OrganizationID:  "demo",
		ActorID:         "reviewer-1",
		CorrelationID:   "run-1",
		Proposal:        ready,
		Hypothesis:      "The proposed change raises protected task pass rate without regressing latency.",
		IntendedOutcome: "Pass rate is at least 0.90 and p95 latency is at most 2 seconds.",
		Executor: ExecutorIdentity{
			Agent: "codex", Model: "gpt-5.6", Harness: "fixture-harness@1", Toolchain: "go1.26",
		},
		EvalSuite: EvalSuite{
			ID: "release-evals", Version: "2026-09-14",
			Protected: []ProtectedEval{
				{Name: "latency", Metric: "p95_seconds", Comparator: "lte", Threshold: 2},
				{Name: "quality", Metric: "pass_rate", Comparator: "gte", Threshold: 0.90},
			},
		},
		Budget: Budget{Currency: "GBP", MaxCostMicrounits: 2_000_000, MaxRuntimeSeconds: 900, MaxInputTokens: 100_000, MaxOutputTokens: 20_000},
	}
	first, err := experiments.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := experiments.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.SpecRevision != second.SpecRevision || first.HandoffSHA256 != second.HandoffSHA256 {
		t.Fatalf("identical experiment inputs were not deterministic: first=%+v second=%+v", first, second)
	}
	if first.Base.RevisionID != info.RevisionID || first.Base.Commit != info.Commit || first.Base.Tree != info.Tree || first.Origin.ProposalID != ready.ID || first.Origin.CandidateID != ready.CandidateID {
		t.Fatalf("experiment lost exact provenance: %+v", first)
	}
	if first.Status != StatusIssued || !strings.HasPrefix(first.SpecRevision, "sha256:") || len(first.HandoffSHA256) != 64 {
		t.Fatalf("unexpected issued experiment identity/status: %+v", first)
	}
	if len(first.EvalSuite.Protected) != 2 || first.EvalSuite.Protected[0].Name != "latency" || first.EvalSuite.Protected[1].Name != "quality" {
		t.Fatalf("protected evals were not canonicalized deterministically: %+v", first.EvalSuite.Protected)
	}

	handoffJSON, err := json.Marshal(first.Handoff)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"must-not-leak", "api_key=", "token=", "diff --git", "proposed patch body"} {
		if strings.Contains(strings.ToLower(string(handoffJSON)), strings.ToLower(forbidden)) {
			t.Fatalf("canonical experiment handoff leaked forbidden content %q: %s", forbidden, handoffJSON)
		}
	}
	if first.Origin.ExternalReference != "" {
		t.Fatalf("credential-bearing/signed proposal reference should be omitted when patch digest is available: %q", first.Origin.ExternalReference)
	}
	if len(first.Origin.Evidence) != 1 || first.Origin.Evidence[0].SummarySHA256 == "" {
		t.Fatalf("bounded evidence reference was not preserved: %+v", first.Origin.Evidence)
	}

	changed := input
	changed.Hypothesis = "A materially different hypothesis must issue a different immutable experiment."
	third, err := experiments.Create(ctx, changed)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == first.ID || third.SpecRevision == first.SpecRevision {
		t.Fatalf("changed experiment specification mutated/reused historical identity: first=%s/%s third=%s/%s", first.ID, first.SpecRevision, third.ID, third.SpecRevision)
	}
}

func TestResultIntakeFailsClosedAndPreservesCanonicalCapabilityState(t *testing.T) {
	ctx := context.Background()
	catalog, info, ready := experimentFixture(t, ctx)
	experiments, err := New(ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	create := CreateInput{
		OrganizationID: "demo", ActorID: "reviewer-1", Proposal: ready,
		Hypothesis: "The candidate improves protected release quality within the latency gate.",
		EvalSuite: EvalSuite{ID: "release-evals", Version: "v7", Protected: []ProtectedEval{
			{Name: "quality", Metric: "pass_rate", Comparator: "gte", Threshold: 0.90},
			{Name: "latency", Metric: "p95_seconds", Comparator: "lte", Threshold: 2.0},
		}},
		Budget: Budget{MaxRuntimeSeconds: 600},
	}
	issued, err := experiments.Create(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	staleCandidateInput := create
	staleCandidateInput.Hypothesis = "A second issued experiment remains bound to the old base if upstream changes."
	staleCandidate, err := experiments.Create(ctx, staleCandidateInput)
	if err != nil {
		t.Fatal(err)
	}

	before := canonicalSkillState(t, ctx, catalog, info.SkillID)
	baseResult := SubmitResultInput{
		OrganizationID: "demo", ActorID: "runner-intake", CorrelationID: "result-1",
		ExperimentID: issued.ID, SpecRevision: issued.SpecRevision, HandoffSHA256: issued.HandoffSHA256,
		EvalSuiteID: issued.EvalSuite.ID, EvalSuiteVersion: issued.EvalSuite.Version,
		Measurements: []Measurement{
			{Name: "quality", Value: 0.93, EvidenceRef: "sha256:" + strings.Repeat("c", 64)},
			{Name: "latency", Value: 1.7, EvidenceRef: "https://ci.example.invalid/artifacts/latency.json"},
		},
		Artifacts: []ArtifactReference{{Name: "report", Reference: "https://ci.example.invalid/artifacts/report.json", SHA256: strings.Repeat("d", 64)}},
		Summary:   "Protected evals completed within the declared experiment contract.",
	}

	mismatch := baseResult
	mismatch.SpecRevision = "sha256:" + strings.Repeat("0", 64)
	if _, err := experiments.SubmitResult(ctx, mismatch); err == nil || !strings.Contains(err.Error(), "exact experiment spec revision") {
		t.Fatalf("mismatched result did not fail closed: %v", err)
	}
	if afterMismatch, err := experiments.Get(ctx, "demo", issued.ID); err != nil || afterMismatch.Status != StatusIssued || afterMismatch.Result != nil {
		t.Fatalf("mismatched result changed issued experiment: item=%+v err=%v", afterMismatch, err)
	}

	missingProtected := baseResult
	missingProtected.Measurements = missingProtected.Measurements[:1]
	if _, err := experiments.SubmitResult(ctx, missingProtected); err == nil || !strings.Contains(err.Error(), "exactly one measurement") {
		t.Fatalf("omitting a protected threshold did not fail closed: %v", err)
	}

	completed, err := experiments.SubmitResult(ctx, baseResult)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != StatusCompleted || completed.Result == nil || len(completed.Result.Outcomes) != 2 {
		t.Fatalf("passing protected measurements did not complete experiment: %+v", completed)
	}
	for _, outcome := range completed.Result.Outcomes {
		switch outcome.Name {
		case "quality":
			if outcome.Threshold != 0.90 || !outcome.Passed {
				t.Fatalf("quality threshold was weakened or mis-evaluated: %+v", outcome)
			}
		case "latency":
			if outcome.Threshold != 2.0 || !outcome.Passed {
				t.Fatalf("latency threshold was weakened or mis-evaluated: %+v", outcome)
			}
		default:
			t.Fatalf("unexpected protected eval outcome: %+v", outcome)
		}
	}
	if after := canonicalSkillState(t, ctx, catalog, info.SkillID); after != before {
		t.Fatalf("experiment completion changed canonical capability state: before=%+v after=%+v", before, after)
	}

	admitNextRevision(t, ctx, catalog)
	staleResult := baseResult
	staleResult.ExperimentID = staleCandidate.ID
	staleResult.SpecRevision = staleCandidate.SpecRevision
	staleResult.HandoffSHA256 = staleCandidate.HandoffSHA256
	if _, err := experiments.SubmitResult(ctx, staleResult); !errors.Is(err, ErrStaleBase) {
		t.Fatalf("stale-base result was not rejected: %v", err)
	}
	stale, err := experiments.Get(ctx, "demo", staleCandidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Status != StatusStale || stale.Result != nil {
		t.Fatalf("stale experiment accepted result evidence: %+v", stale)
	}
	retained, err := experiments.Get(ctx, "demo", issued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retained.Status != StatusCompleted || retained.Result == nil {
		t.Fatalf("historical completed evidence was rewritten after upstream changed: %+v", retained)
	}
}

func TestSafeResultReferencesRejectExecutableAuthorityAndSecrets(t *testing.T) {
	for _, valid := range []string{
		"https://ci.example.invalid/artifacts/result.json",
		"sha256:" + strings.Repeat("a", 64),
	} {
		if err := validateSafeReference(valid); err != nil {
			t.Fatalf("safe reference %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"https://user:secret@ci.example.invalid/result",
		"https://ci.example.invalid/result?token=must-not-leak",
		"https://ci.example.invalid/result#bearer",
		"file:///tmp/result.json",
		"https://ci.example.invalid/password:must-not-leak",
	} {
		if err := validateSafeReference(invalid); err == nil {
			t.Fatalf("unsafe reference %q was accepted", invalid)
		}
	}
}

type skillState struct {
	ActiveRevisionID string
	Searchable       int
	Owner            string
}

func canonicalSkillState(t *testing.T, ctx context.Context, catalog *catalogue.Store, skillID string) skillState {
	t.Helper()
	var state skillState
	if err := catalog.DB.QueryRowContext(ctx, `SELECT active_revision_id, searchable, owner FROM skills WHERE id=?`, skillID).Scan(&state.ActiveRevisionID, &state.Searchable, &state.Owner); err != nil {
		t.Fatal(err)
	}
	return state
}

func experimentFixture(t *testing.T, ctx context.Context) (*catalogue.Store, catalogue.RevisionInfo, proposal.Proposal) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)
	tarDigest, zipDigest := putExperimentPackage(t, packages)
	repo := catalogue.Repository{ID: "central", OrganizationID: "demo", URL: "https://example.invalid/skills", Ref: "main", TrustLevel: "approved", Owner: "platform-team"}
	skill := discovery.Skill{RelativePath: "release", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "release", Description: "release experiment fixture"}}
	revision, err := catalog.Admit(ctx, repo, skill, "commit-experiment-1", "tree-experiment-1", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	info, err := catalog.Revision(ctx, "demo", revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	ready := proposal.Proposal{
		ID: "prop_fixture", OrganizationID: "demo", CapabilityID: info.SkillID,
		Base: proposal.Base{
			StableCapabilityID: info.SkillID, RevisionID: info.RevisionID, Commit: info.Commit, Tree: info.Tree,
			ArchiveSHA256TarGZ: info.ArchiveSHA256TarGZ, ArchiveSHA256ZIP: info.ArchiveSHA256ZIP,
			RepositoryID: info.RepositoryID, RepositoryURL: info.RepositoryURL, SourcePath: info.Path,
		},
		CandidateID: "cand_fixture",
		Evidence: []evidence.EvidenceReference{{
			Kind: "feedback", ID: 7, Signal: "workaround_required", RevisionID: info.RevisionID,
			ArchiveSHA256: info.ArchiveSHA256TarGZ, MaterializationID: "mat_fixture",
			SummaryExcerpt: "api_key=must-not-leak", SummarySHA256: strings.Repeat("a", 64),
		}},
		IntendedOutcome:   "Improve the protected release task while preserving existing regression thresholds.",
		Patch:             "proposed patch body that must not be copied into an experiment handoff",
		PatchSHA256:       strings.Repeat("b", 64),
		ExternalReference: "https://github.example.invalid/review/1?token=must-not-leak",
		Status:            proposal.StatusReadyForReview,
		ActorID:           "reviewer-1",
	}
	return catalog, info, ready
}

func putExperimentPackage(t *testing.T, packages *packagestore.Store) (string, string) {
	t.Helper()
	markdown := "---\nname: release\ndescription: release experiment fixture\n---\n\n# Release\n"
	contents := map[string][]byte{"release/SKILL.md": []byte(markdown)}
	built, err := packagebuilder.Build("release", "release", []packagebuilder.Entry{{Path: "release/SKILL.md", Kind: packagebuilder.Regular, Mode: 0o644, Size: int64(len(markdown))}}, func(path string) ([]byte, error) {
		value, ok := contents[path]
		if !ok {
			return nil, fmt.Errorf("fixture path %s not found", path)
		}
		return value, nil
	}, packagebuilder.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	tarDigest, err := packages.Put("tar.gz", built.TarGZ)
	if err != nil {
		t.Fatal(err)
	}
	zipDigest, err := packages.Put("zip", built.ZIP)
	if err != nil {
		t.Fatal(err)
	}
	return tarDigest, zipDigest
}

func admitNextRevision(t *testing.T, ctx context.Context, catalog *catalogue.Store) {
	t.Helper()
	repo := catalogue.Repository{ID: "central", OrganizationID: "demo", URL: "https://example.invalid/skills", Ref: "main", TrustLevel: "approved", Owner: "platform-team"}
	skill := discovery.Skill{RelativePath: "release", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "release", Description: "release experiment fixture"}}
	// Reuse the retained immutable package: only source commit/tree identity changes.
	var tarDigest, zipDigest string
	if err := catalog.DB.QueryRowContext(ctx, `SELECT archive_sha256_tar_gz, archive_sha256_zip FROM skill_revisions WHERE skill_id=? ORDER BY admitted_at LIMIT 1`, "demo/central/release").Scan(&tarDigest, &zipDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Admit(ctx, repo, skill, "commit-experiment-2", "tree-experiment-2", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest}); err != nil {
		t.Fatal(err)
	}
}
