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

func TestAdmitPersistsOrganisationSkillRevisionAndActivePointer(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	skill := discovery.Skill{RelativePath: "plan", Entrypoint: "plan/SKILL.md", State: discovery.Admitted, Searchable: true}
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	tarDigest, err := packages.Put("tar.gz", []byte("tar"))
	if err != nil {
		t.Fatal(err)
	}
	zipDigest, err := packages.Put("zip", []byte("zip"))
	if err != nil {
		t.Fatal(err)
	}
	rev, err := New(db, packages).Admit(context.Background(), Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.com/skills.git", Ref: "refs/heads/main", TrustLevel: "approved", Owner: "team"}, skill, "commit1", "tree1", PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	if rev.ID != RevisionID("demo", "skills", "plan", "commit1", "tree1") {
		t.Fatalf("revision = %+v", rev)
	}
	var active string
	if err := db.QueryRow("SELECT active_revision_id FROM skills WHERE id=?", "demo/skills/plan").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != rev.ID {
		t.Fatalf("active = %q", active)
	}
	var event string
	if err := db.QueryRow("SELECT event_type FROM audit_events WHERE organization_id=?", "demo").Scan(&event); err != nil {
		t.Fatal(err)
	}
	if event != "skill_admitted" {
		t.Fatal(event)
	}
}

func TestEnsureRepositorySupportsEmptySourceAudit(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := New(db).EnsureRepository(context.Background(), Repository{ID: "skills", OrganizationID: "demo", URL: "file:///tmp/skills", Ref: "working-tree", TrustLevel: "approved", Owner: "local"}); err != nil {
		t.Fatal(err)
	}
	if err := New(db).RecordAudit(context.Background(), "demo", "repository_sync_started", map[string]any{"repository_id": "demo/skills"}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveVersionSelectsHighestStableAndRejectsAmbiguity(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	catalog := New(db, packages)
	for i, version := range []string{"1.2.0", "1.4.0", "2.0.0-beta.1"} {
		tar, _ := packages.Put("tar.gz", []byte("tar"+version))
		zip, _ := packages.Put("zip", []byte("zip"+version))
		skill := discovery.Skill{RelativePath: "plan", State: discovery.Admitted, Searchable: true, Version: version, Frontmatter: skillspec.Frontmatter{Name: "plan", Description: "plan", Metadata: map[string]string{"version": version}}}
		if _, err := catalog.Admit(context.Background(), Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.com", Ref: "main"}, skill, "commit"+string(rune('a'+i)), "tree"+string(rune('a'+i)), PackageDigests{TarGZ: tar, ZIP: zip}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := catalog.ResolveVersion(context.Background(), "demo", "demo/skills/plan", "", "^1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "1.4.0" {
		t.Fatalf("range selected %q", info.Version)
	}
	if _, err := catalog.ResolveVersion(context.Background(), "demo", "demo/skills/plan", "1.3.0", ""); err == nil {
		t.Fatal("no-match exact version was accepted")
	}
	if _, err := catalog.ResolveVersion(context.Background(), "demo", "demo/skills/plan", "", "not-a-range"); err == nil {
		t.Fatal("malformed range was accepted")
	}
}

func TestRecordQuarantinePreservesVersionMetadata(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	catalog := New(db)
	skill := discovery.Skill{RelativePath: "bad", State: discovery.Quarantined, Version: "01.2.3", Frontmatter: skillspec.Frontmatter{Name: "bad", Description: "Bad", Metadata: map[string]string{"version": "01.2.3", "source": "fixture"}}, Findings: []skillspec.Finding{{Code: skillspec.FindingInvalidVersion, Message: "invalid"}}}
	if err := catalog.RecordQuarantine(context.Background(), Repository{ID: "skills", OrganizationID: "demo"}, skill, "commit", "tree"); err != nil {
		t.Fatal(err)
	}
	var version, metadata string
	if err := db.QueryRow("SELECT version, metadata_json FROM skill_revisions WHERE state='quarantined'").Scan(&version, &metadata); err != nil {
		t.Fatal(err)
	}
	if version != "01.2.3" || !strings.Contains(metadata, "source") {
		t.Fatalf("quarantined provenance = version %q metadata %q", version, metadata)
	}
}

func TestRecordAuditPersistsStructuredIdentityFields(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	catalog := New(db)
	if _, err := db.Exec("INSERT INTO organizations(id) VALUES ('demo')"); err != nil {
		t.Fatal(err)
	}
	if err := catalog.RecordAudit(context.Background(), "demo", "search_executed", map[string]any{"repository_id": "repo", "skill_id": "demo/repo/plan", "revision_id": "rev", "actor_type": "agent"}); err != nil {
		t.Fatal(err)
	}
	var actor, repository, skill, revision string
	if err := db.QueryRow("SELECT actor_type, repository_id, skill_id, revision_id FROM audit_events WHERE event_type='search_executed'").Scan(&actor, &repository, &skill, &revision); err != nil {
		t.Fatal(err)
	}
	if actor != "agent" || repository != "repo" || skill != "demo/repo/plan" || revision != "rev" {
		t.Fatalf("audit identity = %q %q %q %q", actor, repository, skill, revision)
	}
}

func TestAdmitRejectsQuarantinedSkill(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = New(db).Admit(context.Background(), Repository{ID: "skills", OrganizationID: "demo"}, discovery.Skill{State: discovery.Quarantined}, "c", "t", PackageDigests{TarGZ: "a", ZIP: "b"})
	if err == nil {
		t.Fatal("expected quarantine rejection")
	}
}

func TestRoutingDocumentsFiltersConfiguredMetadata(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	tarDigest, _ := packages.Put("tar.gz", []byte("tar-filter"))
	zipDigest, _ := packages.Put("zip", []byte("zip-filter"))
	skill := discovery.Skill{RelativePath: "plan", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "plan", Description: "plan work", Metadata: map[string]string{"public": "yes", "secret": "no"}}}
	if _, err := New(db, packages).Admit(context.Background(), Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.com/skills.git", Ref: "main", Owner: "team"}, skill, "commit-filter", "tree-filter", PackageDigests{TarGZ: tarDigest, ZIP: zipDigest}); err != nil {
		t.Fatal(err)
	}
	docs, err := New(db, packages).RoutingDocuments(context.Background(), "demo", []string{"public"})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Metadata["public"] != "yes" {
		t.Fatalf("docs = %+v", docs)
	}
	if docs[0].OrganizationID != "demo" || docs[0].RepositoryID != "skills" {
		t.Fatalf("document identity = %+v", docs[0])
	}
	if _, ok := docs[0].Metadata["secret"]; ok {
		t.Fatalf("secret metadata leaked: %+v", docs[0].Metadata)
	}
}

func TestMarkMissingFromSourceRemovesOnlyAbsentActiveSkills(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	catalog := New(db, packages)
	for _, path := range []string{"keep", "remove"} {
		tar, _ := packages.Put("tar.gz", []byte("tar-"+path))
		zip, _ := packages.Put("zip", []byte("zip-"+path))
		if _, err := catalog.Admit(context.Background(), Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.com/skills.git", Ref: "main", Owner: "team"}, discovery.Skill{RelativePath: path, State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: path, Description: path}}, "commit-"+path, "tree-"+path, PackageDigests{TarGZ: tar, ZIP: zip}); err != nil {
			t.Fatal(err)
		}
	}
	if err := catalog.MarkMissingFromSource(context.Background(), Repository{ID: "skills", OrganizationID: "demo"}, map[string]struct{}{"keep": {}}); err != nil {
		t.Fatal(err)
	}
	docs, err := catalog.RoutingDocuments(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Name != "keep" {
		t.Fatalf("remaining docs = %+v", docs)
	}
	var state string
	if err := db.QueryRow("SELECT state FROM skill_revisions WHERE skill_id=?", "demo/skills/remove").Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "removed_from_source" {
		t.Fatalf("state = %q", state)
	}
	if _, err := catalog.Revision(context.Background(), "demo", RevisionID("demo", "skills", "remove", "commit-remove", "tree-remove")); err != nil {
		t.Fatalf("removed historical revision was not restorable: %v", err)
	}
}
