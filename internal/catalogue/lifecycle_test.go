package catalogue

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
)

func TestRecordLifecycleBindsObservationToImmutableMaterialization(t *testing.T) {
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
		"skill_id": rev.SkillID, "revision_id": rev.ID, "archive_sha256": tarDigest,
		"request_id": "materialize-1",
	}); err != nil {
		t.Fatal(err)
	}
	observation := LifecycleObservation{RevisionID: rev.ID, SkillID: rev.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: tarDigest, MaterializationID: "materialize-1", Event: "activated", CorrelationID: "session-1", Source: "pi"}
	if err := catalog.RecordLifecycle(ctx, "demo", observation); err != nil {
		t.Fatal(err)
	}
	var event, revisionID, actorID, details string
	if err := db.QueryRow(`SELECT event_type, revision_id, actor_id, details_json FROM audit_events WHERE event_type='skill_activated'`).Scan(&event, &revisionID, &actorID, &details); err != nil {
		t.Fatal(err)
	}
	if event != "skill_activated" || revisionID != rev.ID || actorID != "pi" {
		t.Fatalf("event=%q revision=%q actor=%q", event, revisionID, actorID)
	}
	if !strings.Contains(details, `"correlation_id":"session-1"`) || !strings.Contains(details, `"materialization_id":"materialize-1"`) {
		t.Fatalf("details = %s", details)
	}
}

func TestRecordLifecycleRejectsForgedProvenance(t *testing.T) {
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
		"skill_id": rev.SkillID, "revision_id": rev.ID, "archive_sha256": tarDigest,
		"request_id": "materialize-1",
	}); err != nil {
		t.Fatal(err)
	}
	valid := LifecycleObservation{RevisionID: rev.ID, SkillID: rev.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: tarDigest, MaterializationID: "materialize-1", Event: "activated", Source: "pi"}

	bad := valid
	bad.ArchiveSHA256 = "forged"
	if err := catalog.RecordLifecycle(ctx, "demo", bad); err == nil {
		t.Fatal("forged lifecycle identity was accepted")
	}
	bad = valid
	bad.MaterializationID = "forged-materialization"
	if err := catalog.RecordLifecycle(ctx, "demo", bad); err == nil {
		t.Fatal("forged materialization provenance was accepted")
	}
	bad = valid
	bad.Event = "used"
	if err := catalog.RecordLifecycle(ctx, "demo", bad); err == nil {
		t.Fatal("unsupported lifecycle event was accepted")
	}
	if err := catalog.RecordLifecycle(ctx, "other", valid); err == nil {
		t.Fatal("cross-organization lifecycle observation was accepted")
	}
}
