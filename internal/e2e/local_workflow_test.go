package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/adapter"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/gitstore"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/ingest"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
)

func TestOfflineLocalAdmissionSearchAndMaterialize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	root := t.TempDir()
	sourceRoot := filepath.Join(root, "fixture-skills")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	const skillBody = "---\nname: plan\ndescription: Create a concrete implementation plan before changing code.\n---\n# Plan\n\nBuild the smallest verified slice.\n"
	if err := os.WriteFile(filepath.Join(sourceRoot, "plan", "SKILL.md"), []byte(skillBody), 0o644); err != nil {
		t.Fatal(err)
	}

	source, err := gitstore.NewLocalSource(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := source.Fetch(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)
	repo := catalogue.Repository{
		ID:             "fixture-skills",
		OrganizationID: "demo",
		URL:            "file://" + filepath.ToSlash(sourceRoot),
		Ref:            "local",
		TrustLevel:     "approved",
		Owner:          "verification",
	}
	result, err := ingest.SyncAtCommitWithOptions(ctx, source, repo, packages, catalog, commit, ingest.Options{Include: []string{"**/SKILL.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Admitted != 1 || result.Quarantined != 0 {
		t.Fatalf("admission = %+v, want one admitted skill and no quarantine", result)
	}

	docs, err := catalog.RoutingDocuments(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Name != "plan" {
		t.Fatalf("routing documents = %+v", docs)
	}
	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Rebuild(docs); err != nil {
		t.Fatal(err)
	}
	hits, degraded, err := index.Search("create an implementation plan", 50, 50, 5, 60)
	if err != nil {
		t.Fatal(err)
	}
	if !degraded || len(hits) == 0 || hits[0].ID != docs[0].ID {
		t.Fatalf("offline lexical fallback degraded=%v hits=%+v", degraded, hits)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String()
	app := httpserver.NewComplete(nil, nil, index, "demo", candidate.Signer{Key: []byte("candidate-verification-key")}, packages, packageurl.Signer{Key: []byte("package-verification-key")}, catalog, baseURL)
	httpSrv := &http.Server{Handler: app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "demo"})}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(listener) }()
	t.Cleanup(func() {
		_ = httpSrv.Shutdown(context.Background())
		select {
		case err := <-serveErr:
			if err != nil && err != http.ErrServerClosed {
				t.Errorf("local Skillet server: %v", err)
			}
		default:
		}
	})

	client := adapter.Client{Server: baseURL + "/mcp"}
	found, err := client.Search(ctx, "create an implementation plan", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Candidates) != 1 || found.Candidates[0].CandidateID == "" || found.Candidates[0].Skill.Name != "plan" {
		t.Fatalf("search result = %+v", found)
	}

	materialized, entrypoint, err := client.Materialize(ctx, found.Candidates[0].CandidateID, filepath.Join(root, "materialized"))
	if err != nil {
		t.Fatal(err)
	}
	if materialized.Lifecycle.RevisionID == "" || materialized.Lifecycle.Commit != commit || materialized.Lifecycle.Tree == "" {
		t.Fatalf("immutable lifecycle provenance = %+v", materialized.Lifecycle)
	}
	if materialized.Package.ArchiveSHA256 == "" || materialized.Package.ArchiveSHA256 != materialized.Lifecycle.ArchiveSHA256 {
		t.Fatalf("package/lifecycle digest mismatch: package=%+v lifecycle=%+v", materialized.Package, materialized.Lifecycle)
	}
	contents, err := os.ReadFile(entrypoint)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != skillBody {
		t.Fatalf("materialized SKILL.md differs from admitted fixture: %q", contents)
	}

	archive, err := packages.Get(materialized.Package.Format, materialized.Package.ArchiveSHA256)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	if got := hex.EncodeToString(digest[:]); got != materialized.Package.ArchiveSHA256 {
		t.Fatalf("retained archive digest = %s, want %s", got, materialized.Package.ArchiveSHA256)
	}

	revision, err := catalog.Revision(ctx, "demo", materialized.Lifecycle.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if revision.Commit != materialized.Lifecycle.Commit || revision.Tree != materialized.Lifecycle.Tree || revision.SkillID != materialized.Lifecycle.SkillID {
		t.Fatalf("materialization provenance does not match admitted revision: revision=%+v lifecycle=%+v", revision, materialized.Lifecycle)
	}
}
