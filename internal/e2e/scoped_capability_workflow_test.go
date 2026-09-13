package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/adapter"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/gitstore"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/ingest"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
)

func TestOfflineScopedCapabilityDiscoveryAndMaterialization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	root := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)

	syncCapabilityFixture(t, ctx, root, "central", "release", "Plan and verify a production release for any repository.", catalog, packages)
	syncCapabilityFixture(t, ctx, root, "local-a", "release", "Plan repository A deployment, migration and release work.", catalog, packages)
	syncCapabilityFixture(t, ctx, root, "local-b", "release", "Plan repository B deployment, migration and release work.", catalog, packages)

	docs, err := catalog.RoutingDocuments(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("routing documents = %d, want 3", len(docs))
	}
	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Rebuild(docs); err != nil {
		t.Fatal(err)
	}
	capabilities, err := capability.New(index, []capability.SourcePolicy{
		{RepositoryID: "local-a", Scope: mustCapabilityScope(t, "demo", "team", "repo-a")},
		{RepositoryID: "local-b", Scope: mustCapabilityScope(t, "demo", "team", "repo-b")},
	})
	if err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String()
	app := httpserver.NewComplete(nil, nil, index, "demo", candidate.Signer{Key: []byte("candidate-scope-key")}, packages, packageurl.Signer{Key: []byte("package-scope-key")}, catalog, baseURL)
	app.ConfigureCapabilities(capabilities)
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
	aScope := adapter.CapabilityScope{Namespace: "team", Repository: "repo-a"}
	bScope := adapter.CapabilityScope{Namespace: "team", Repository: "repo-b"}

	a, err := client.SearchCapabilities(ctx, "repository A deployment migration release", aScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertCapabilityRepositories(t, a, map[string]bool{"central": true, "local-a": true}, "local-b")
	b, err := client.SearchCapabilities(ctx, "repository B deployment migration release", bScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertCapabilityRepositories(t, b, map[string]bool{"central": true, "local-b": true}, "local-a")

	centralOnly, err := client.SearchCapabilities(ctx, "production release any repository", adapter.CapabilityScope{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertCapabilityRepositories(t, centralOnly, map[string]bool{"central": true}, "local-a", "local-b")

	if _, err := client.SearchCapabilities(ctx, "release", adapter.CapabilityScope{Namespace: "team", Repository: "../repo-b"}, 10); err == nil {
		t.Fatal("malformed scope unexpectedly broadened capability search")
	}

	localCandidate := findCapabilityCandidate(t, a, "local-a")
	centralCandidate := findCapabilityCandidate(t, a, "central")
	localDetail, err := client.DescribeCapability(ctx, localCandidate.CandidateID, aScope)
	if err != nil {
		t.Fatal(err)
	}
	if localDetail.Detail.Descriptor.Provenance.ArchiveSHA256TarGZ == "" || localDetail.MaterializeCandidateID != localCandidate.CandidateID {
		t.Fatalf("local describe lost immutable materialisation linkage: %+v", localDetail)
	}
	if _, err := client.DescribeCapability(ctx, localCandidate.CandidateID, bScope); err == nil {
		t.Fatal("repository A capability described in repository B scope")
	}

	localMaterialized, _, err := client.Materialize(ctx, localCandidate.CandidateID, filepath.Join(root, "materialized-local"))
	if err != nil {
		t.Fatal(err)
	}
	if localMaterialized.Lifecycle.RevisionID != localCandidate.Capability.Provenance.RevisionID || localMaterialized.Lifecycle.Commit != localCandidate.Capability.Provenance.Commit || localMaterialized.Lifecycle.Tree != localCandidate.Capability.Provenance.Tree || localMaterialized.Package.ArchiveSHA256 != localDetail.Detail.Descriptor.Provenance.ArchiveSHA256TarGZ {
		t.Fatalf("local materialization provenance mismatch: materialized=%+v candidate=%+v detail=%+v", localMaterialized, localCandidate, localDetail)
	}

	centralDetail, err := client.DescribeCapability(ctx, centralCandidate.CandidateID, aScope)
	if err != nil {
		t.Fatal(err)
	}
	centralMaterialized, _, err := client.Materialize(ctx, centralCandidate.CandidateID, filepath.Join(root, "materialized-central"))
	if err != nil {
		t.Fatal(err)
	}
	if centralMaterialized.Lifecycle.RevisionID != centralCandidate.Capability.Provenance.RevisionID || centralMaterialized.Package.ArchiveSHA256 != centralDetail.Detail.Descriptor.Provenance.ArchiveSHA256TarGZ {
		t.Fatalf("central materialization provenance mismatch: materialized=%+v candidate=%+v detail=%+v", centralMaterialized, centralCandidate, centralDetail)
	}

	// The v1 surface remains present and can still discover/materialize skills.
	legacy, err := client.Search(ctx, "production release", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.Candidates) == 0 || legacy.Candidates[0].CandidateID == "" {
		t.Fatalf("v1 search_skills compatibility result = %+v", legacy)
	}
}

func syncCapabilityFixture(t *testing.T, ctx context.Context, root, repositoryID, skillName, description string, catalog *catalogue.Store, packages *packagestore.Store) {
	t.Helper()
	sourceRoot := filepath.Join(root, repositoryID)
	skillDir := filepath.Join(sourceRoot, skillName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n# %s\n\nDeterministic scoped capability fixture.\n", skillName, description, skillName)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
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
	repo := catalogue.Repository{ID: repositoryID, OrganizationID: "demo", URL: "file://" + filepath.ToSlash(sourceRoot), Ref: "local", TrustLevel: "approved", Owner: "verification"}
	result, err := ingest.SyncAtCommitWithOptions(ctx, source, repo, packages, catalog, commit, ingest.Options{Include: []string{"**/SKILL.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Admitted != 1 || result.Quarantined != 0 {
		t.Fatalf("%s admission = %+v", repositoryID, result)
	}
}

func mustCapabilityScope(t *testing.T, organization, namespace, repository string) capability.Scope {
	t.Helper()
	s, err := capability.NewScope(organization, namespace, repository)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func assertCapabilityRepositories(t *testing.T, result adapter.CapabilitySearchResult, want map[string]bool, forbidden ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, candidate := range result.Candidates {
		seen[candidate.Capability.Source.RepositoryID] = true
	}
	for repository := range want {
		if !seen[repository] {
			t.Fatalf("repository %s missing from candidates %+v", repository, result.Candidates)
		}
	}
	for _, repository := range forbidden {
		if seen[repository] {
			t.Fatalf("repository %s leaked into candidates %+v", repository, result.Candidates)
		}
	}
}

func findCapabilityCandidate(t *testing.T, result adapter.CapabilitySearchResult, repositoryID string) struct {
	CandidateID string                `json:"candidate_id"`
	Capability  capability.Descriptor `json:"capability"`
	Ranking     search.Hit            `json:"ranking"`
} {
	t.Helper()
	for _, candidate := range result.Candidates {
		if candidate.Capability.Source.RepositoryID == repositoryID {
			return candidate
		}
	}
	t.Fatalf("repository %s missing from %+v", repositoryID, result.Candidates)
	return struct {
		CandidateID string                `json:"candidate_id"`
		Capability  capability.Descriptor `json:"capability"`
		Ranking     search.Hit            `json:"ranking"`
	}{}
}
