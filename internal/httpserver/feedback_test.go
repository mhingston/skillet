package httpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
)

func TestFeedbackToolRecordsAndListsBoundedObservation(t *testing.T) {
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
	rev, err := catalog.Admit(ctx, catalogue.Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.com", Ref: "main"}, skill, "commit1", "tree1", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.RecordAudit(ctx, "demo", "materialisation_prepared", map[string]any{
		"skill_id": rev.SkillID, "revision_id": rev.ID, "archive_sha256": tarDigest, "request_id": "materialize-1",
	}); err != nil {
		t.Fatal(err)
	}
	s := &Server{catalogue: catalog, organizationID: "demo", metrics: &Metrics{}}
	_, out, err := s.feedbackTool(ctx, nil, feedbackInput{
		Lifecycle: lifecycleReference{RevisionID: rev.ID, SkillID: rev.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: tarDigest, MaterializationID: "materialize-1"},
		Category: "user_correction", Summary: "The expected output format was ambiguous.", CorrelationID: "run-1", Source: "pi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "recorded" || out.FeedbackID == 0 || out.RevisionID != rev.ID {
		t.Fatalf("out = %+v", out)
	}
	_, listed, err := s.listFeedbackTool(ctx, nil, listFeedbackInput{RevisionID: rev.ID, Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Feedback) != 1 || listed.Feedback[0].Category != "user_correction" || listed.HasMore {
		t.Fatalf("listed = %+v", listed)
	}
}

func TestFeedbackToolRejectsUnboundedPayloads(t *testing.T) {
	s := &Server{catalogue: &catalogue.Store{}, organizationID: "demo", metrics: &Metrics{}}
	if _, _, err := s.feedbackTool(context.Background(), nil, feedbackInput{Category: "step_failed", Summary: strings.Repeat("x", 1001)}); err == nil {
		t.Fatal("oversized feedback summary was accepted")
	}
	if _, _, err := s.feedbackTool(context.Background(), nil, feedbackInput{Category: "step_failed", Summary: "   "}); err == nil {
		t.Fatal("empty feedback summary was accepted")
	}
	if _, _, err := s.listFeedbackTool(context.Background(), nil, listFeedbackInput{}); err == nil {
		t.Fatal("unscoped feedback listing was accepted")
	}
}
