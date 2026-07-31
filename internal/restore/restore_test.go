package restore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/lockfile"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/store"
)

func TestRestoreResolvesLockedHistoricalRevisionAndSignsExactPackage(t *testing.T) {
	r := fixtureRestorer(t)
	first := admitFixture(t, r, "commit-one", "tree-one", []byte("first"))
	_ = admitFixture(t, r, "commit-two", "tree-two", []byte("second"))

	entry := lockEntry(first, "tar.gz", first.ArchiveSHA256TarGZ)
	got, err := r.Restore(context.Background(), lockfile.File{LockfileVersion: 1, Skills: map[string]lockfile.Entry{first.SkillID: entry}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Revision.Commit != "commit-one" {
		t.Fatalf("restored revision = %+v", got)
	}
	if got[0].Digest != entry.Integrity.Archive || got[0].SizeBytes == 0 {
		t.Fatalf("restored package = %+v", got[0])
	}
	payload, err := r.PackageSigner.Verify(got[0].DownloadToken, "demo", r.Now())
	if err != nil {
		t.Fatal(err)
	}
	if payload.Digest != entry.Integrity.Archive || payload.Format != entry.Integrity.Format {
		t.Fatalf("package token = %+v", payload)
	}
}

func TestRestoreRejectsChangedLockedIdentityWithoutActiveSubstitution(t *testing.T) {
	r := fixtureRestorer(t)
	first := admitFixture(t, r, "commit-one", "tree-one", []byte("first"))
	_ = admitFixture(t, r, "commit-two", "tree-two", []byte("second"))
	entry := lockEntry(first, "tar.gz", first.ArchiveSHA256TarGZ)
	entry.Resolved.Commit = "commit-two"

	_, err := r.Restore(context.Background(), lockfile.File{LockfileVersion: 1, Skills: map[string]lockfile.Entry{first.SkillID: entry}})
	if err == nil || !strings.Contains(err.Error(), "locked revision") {
		t.Fatalf("expected locked-revision error, got %v", err)
	}
}

func TestRestoreAcceptsCanonicalRawRepositoryID(t *testing.T) {
	r := fixtureRestorer(t)
	info := admitFixture(t, r, "commit-one", "tree-one", []byte("first"))
	entry := lockEntry(info, "tar.gz", info.ArchiveSHA256TarGZ)
	entry.Source.RepositoryID = "skills"

	got, err := r.Restore(context.Background(), lockfile.File{LockfileVersion: 1, Skills: map[string]lockfile.Entry{info.SkillID: entry}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Revision.Commit != info.Commit {
		t.Fatalf("restored revision = %+v", got)
	}
}

func TestRestoreFailsClosedOnDigestMismatchAndMissingPackage(t *testing.T) {
	r := fixtureRestorer(t)
	info := admitFixture(t, r, "commit-one", "tree-one", []byte("first"))

	badDigest := strings.Repeat("0", 64)
	entry := lockEntry(info, "tar.gz", badDigest)
	if _, err := r.Restore(context.Background(), lockfile.File{LockfileVersion: 1, Skills: map[string]lockfile.Entry{info.SkillID: entry}}); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("expected digest failure, got %v", err)
	}

	missing := lockEntry(info, "zip", info.ArchiveSHA256ZIP)
	path := filepath.Join(r.Packages.Root, "sha256", info.ArchiveSHA256ZIP[:2], info.ArchiveSHA256ZIP+".zip")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Restore(context.Background(), lockfile.File{LockfileVersion: 1, Skills: map[string]lockfile.Entry{info.SkillID: missing}}); err == nil {
		t.Fatal("missing locked package was accepted")
	}
}

func TestRestoreRejectsMalformedOrCrossOrganisationLock(t *testing.T) {
	r := fixtureRestorer(t)
	info := admitFixture(t, r, "commit-one", "tree-one", []byte("first"))
	base := lockEntry(info, "tar.gz", info.ArchiveSHA256TarGZ)
	cases := []struct {
		name  string
		entry lockfile.Entry
	}{
		{"wrong algorithm", func() lockfile.Entry { e := base; e.Integrity.Algorithm = "sha512"; return e }()},
		{"wrong format", func() lockfile.Entry { e := base; e.Integrity.Format = "rar"; return e }()},
		{"wrong source", func() lockfile.Entry { e := base; e.Source.RepositoryID = "other/skills"; return e }()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.Restore(context.Background(), lockfile.File{LockfileVersion: 1, Skills: map[string]lockfile.Entry{info.SkillID: tc.entry}}); err == nil {
				t.Fatal("malformed lock entry was accepted")
			}
		})
	}
}

type fixtureRestorerState struct {
	Catalogue     *catalogue.Store
	Packages      *packagestore.Store
	PackageSigner packageurl.Signer
	Now           func() time.Time
}

func fixtureRestorer(t *testing.T) *Restorer {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	now := time.Unix(1_800_000_000, 0).UTC()
	return &Restorer{
		OrganizationID: "demo",
		Catalogue:      catalogue.New(db, packages),
		Packages:       packages,
		PackageSigner:  packageurl.Signer{Key: []byte("package-key")},
		PublicBaseURL:  "https://skillet.example",
		Now:            func() time.Time { return now },
	}
}

func admitFixture(t *testing.T, r *Restorer, commit, tree string, content []byte) catalogue.RevisionInfo {
	t.Helper()
	tarDigest, err := r.Packages.Put("tar.gz", content)
	if err != nil {
		t.Fatal(err)
	}
	zipDigest, err := r.Packages.Put("zip", append([]byte("zip-"), content...))
	if err != nil {
		t.Fatal(err)
	}
	skill := discovery.Skill{RelativePath: "plan", State: discovery.Admitted, Searchable: true}
	_, err = r.Catalogue.Admit(context.Background(), catalogue.Repository{ID: "skills", OrganizationID: "demo", URL: "https://example/skills.git", Ref: "refs/heads/main", TrustLevel: "approved", Owner: "team"}, skill, commit, tree, catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	info, err := r.Catalogue.Revision(context.Background(), "demo", catalogue.RevisionID("demo", "skills", "plan", commit, tree))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func lockEntry(info catalogue.RevisionInfo, format, digest string) lockfile.Entry {
	return lockfile.Entry{
		Name:      info.Name,
		Source:    lockfile.Source{Type: "git", RepositoryID: info.RepositoryID, RepositoryURL: info.RepositoryURL, Path: info.Path},
		Resolved:  lockfile.Resolved{Commit: info.Commit, Tree: info.Tree},
		Integrity: lockfile.Integrity{Algorithm: "sha256", Archive: digest, Format: format},
	}
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
