package httpserver

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
)

func TestImprovementCandidatesToolReturnsBoundedReadOnlyView(t *testing.T) {
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
	skill := discovery.Skill{RelativePath: "plan", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "plan", Description: "plan"}}
	revision, err := catalog.Admit(ctx, catalogue.Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.com", Ref: "main"}, skill, "commit1", "tree1", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.RecordAudit(ctx, "demo", "materialisation_prepared", map[string]any{"skill_id": revision.SkillID, "revision_id": revision.ID, "archive_sha256": tarDigest, "request_id": "materialize-1"}); err != nil {
		t.Fatal(err)
	}
	ref := catalogue.MaterializationReference{RevisionID: revision.ID, SkillID: revision.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: tarDigest, MaterializationID: "materialize-1"}
	for _, feedback := range []catalogue.FeedbackObservation{
		{Reference: ref, Category: "ambiguous_instruction", Summary: "The rollback instruction was ambiguous.", CorrelationID: "run-1", Source: "fixture"},
		{Reference: ref, Category: "effective_pattern", Summary: "Digest verification was effective.", CorrelationID: "run-2", Source: "fixture"},
	} {
		if _, err := catalog.RecordFeedback(ctx, "demo", feedback); err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{catalogue: catalog, organizationID: "demo", metrics: &Metrics{}}
	_, first, err := s.improvementCandidatesTool(ctx, nil, improvementCandidatesInput{RevisionID: revision.ID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Candidates) != 1 || first.Total != 2 || !first.HasMore || first.Offset != 0 || first.Limit != 1 {
		t.Fatalf("first page = %+v", first)
	}
	_, second, err := s.improvementCandidatesTool(ctx, nil, improvementCandidatesInput{RevisionID: revision.ID, Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Candidates) != 1 || second.Total != 2 || second.HasMore || second.Candidates[0].ID == first.Candidates[0].ID {
		t.Fatalf("second page = %+v", second)
	}
	for _, page := range []improvementCandidatesOutput{first, second} {
		for _, candidate := range page.Candidates {
			if candidate.Provenance.RevisionID != revision.ID || candidate.Handoff.Kind != "github_issue_draft" {
				t.Fatalf("candidate lost review/provenance contract: %+v", candidate)
			}
		}
	}
}

func TestImprovementCandidatesToolRejectsUnboundedOrUnscopedReads(t *testing.T) {
	s := &Server{catalogue: &catalogue.Store{}, organizationID: "demo", metrics: &Metrics{}}
	if _, _, err := s.improvementCandidatesTool(context.Background(), nil, improvementCandidatesInput{}); err == nil {
		t.Fatal("unscoped candidate listing was accepted")
	}
	if _, _, err := s.improvementCandidatesTool(context.Background(), nil, improvementCandidatesInput{RevisionID: "rev", Limit: 51}); err == nil {
		t.Fatal("candidate limit above 50 was accepted")
	}
	if _, _, err := s.improvementCandidatesTool(context.Background(), nil, improvementCandidatesInput{RevisionID: "rev", Offset: -1}); err == nil {
		t.Fatal("negative candidate offset was accepted")
	}
}
