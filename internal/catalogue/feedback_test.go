package catalogue

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
)

func TestRecordAndListFeedbackBindsToMaterialization(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	tarDigest, _ := packages.Put("tar.gz", []byte("tar"))
	zipDigest, _ := packages.Put("zip", []byte("zip"))
	catalog := New(db, packages)
	skill := discovery.Skill{RelativePath: "plan", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "plan", Description: "plan"}}
	rev, err := catalog.Admit(ctx, Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.com", Ref: "main"}, skill, "commit1", "tree1", PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.RecordAudit(ctx, "demo", "materialisation_prepared", map[string]any{
		"skill_id": rev.SkillID, "revision_id": rev.ID, "archive_sha256": tarDigest, "request_id": "materialize-1",
	}); err != nil {
		t.Fatal(err)
	}
	reference := MaterializationReference{RevisionID: rev.ID, SkillID: rev.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: tarDigest, MaterializationID: "materialize-1"}
	record, err := catalog.RecordFeedback(ctx, "demo", FeedbackObservation{
		Reference: reference, Category: "workaround_required", Summary: "The documented command needed an extra environment flag.", CorrelationID: "session-1", Source: "pi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.ID == 0 || record.RevisionID != rev.ID || record.Category != "workaround_required" || record.Source != "pi" {
		t.Fatalf("record = %+v", record)
	}

	records, err := catalog.ListFeedback(ctx, "demo", rev.SkillID, "", "workaround_required", 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Summary != "The documented command needed an extra environment flag." || records[0].MaterializationID != "materialize-1" {
		t.Fatalf("records = %+v", records)
	}
	other, err := catalog.ListFeedback(ctx, "other", rev.SkillID, "", "", 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("cross-organization records = %+v", other)
	}
}

func TestRecordFeedbackRejectsInvalidCategoryAndProvenance(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	tarDigest, _ := packages.Put("tar.gz", []byte("tar"))
	zipDigest, _ := packages.Put("zip", []byte("zip"))
	catalog := New(db, packages)
	skill := discovery.Skill{RelativePath: "plan", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "plan", Description: "plan"}}
	rev, err := catalog.Admit(ctx, Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.com", Ref: "main"}, skill, "commit1", "tree1", PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.RecordAudit(ctx, "demo", "materialisation_prepared", map[string]any{
		"skill_id": rev.SkillID, "revision_id": rev.ID, "archive_sha256": tarDigest, "request_id": "materialize-1",
	}); err != nil {
		t.Fatal(err)
	}
	valid := MaterializationReference{RevisionID: rev.ID, SkillID: rev.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: tarDigest, MaterializationID: "materialize-1"}
	if _, err := catalog.RecordFeedback(ctx, "demo", FeedbackObservation{Reference: valid, Category: "free_form", Summary: "bad"}); err == nil {
		t.Fatal("unsupported feedback category was accepted")
	}
	forged := valid
	forged.MaterializationID = "forged"
	if _, err := catalog.RecordFeedback(ctx, "demo", FeedbackObservation{Reference: forged, Category: "step_failed", Summary: "bad"}); err == nil {
		t.Fatal("forged materialization provenance was accepted")
	}
}
