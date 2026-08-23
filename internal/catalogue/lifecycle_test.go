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

func TestRecordLifecycleBindsObservationToImmutableRevision(t *testing.T) {
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
	observation := LifecycleObservation{RevisionID: rev.ID, SkillID: rev.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: tarDigest, Event: "activated", CorrelationID: "session-1", Source: "pi"}
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
	if !strings.Contains(details, `"correlation_id":"session-1"`) {
		t.Fatalf("details = %s", details)
	}
}

func TestRecordLifecycleRejectsForgedRevisionIdentity(t *testing.T) {
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
	bad := LifecycleObservation{RevisionID: rev.ID, SkillID: rev.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: "forged", Event: "activated", Source: "pi"}
	if err := catalog.RecordLifecycle(ctx, "demo", bad); err == nil {
		t.Fatal("forged lifecycle identity was accepted")
	}
	bad.ArchiveSHA256 = tarDigest
	bad.Event = "used"
	if err := catalog.RecordLifecycle(ctx, "demo", bad); err == nil {
		t.Fatal("unsupported lifecycle event was accepted")
	}
	if err := catalog.RecordLifecycle(ctx, "other", LifecycleObservation{RevisionID: rev.ID, SkillID: rev.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: tarDigest, Event: "activated"}); err == nil {
		t.Fatal("cross-organization lifecycle observation was accepted")
	}
}
