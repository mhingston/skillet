package e2e

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/adapter"
	authn "github.com/mhingston/skillet/internal/auth"
	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/knowledge"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOfflineClaimsAuthorizationAcrossCapabilityKnowledgeAndMaterialization(t *testing.T) {
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

	syncCapabilityFixture(t, ctx, root, "local-a", "release", "Plan repository A deployment migration release work.", catalog, packages)
	syncCapabilityFixture(t, ctx, root, "local-b", "release", "Plan repository B deployment migration release work.", catalog, packages)
	docs, err := catalog.RoutingDocuments(ctx, "demo")
	if err != nil {
		t.Fatal(err)
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

	knowledgeService, err := knowledge.Open(ctx, filepath.Join(root, "knowledge-data"), knowledge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer knowledgeService.Close()
	bundle := filepath.Join(root, "knowledge-bundle")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaimsKnowledgeFixture(t, bundle, "allowed.md", "Allowed policy", "claims authorization knowledge allowed-marker")
	writeClaimsKnowledgeFixture(t, bundle, "denied.md", "Denied policy", "claims authorization knowledge denied-marker")
	if _, err := knowledgeService.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: "claims", Root: bundle, Revision: "rev-1"}}); err != nil {
		t.Fatal(err)
	}
	allowedKnowledge, err := knowledgeService.SearchOKF(ctx, "allowed-marker", 1)
	if err != nil || len(allowedKnowledge.Results) != 1 {
		t.Fatalf("resolve allowed knowledge: results=%+v err=%v", allowedKnowledge.Results, err)
	}
	deniedKnowledge, err := knowledgeService.SearchOKF(ctx, "denied-marker", 1)
	if err != nil || len(deniedKnowledge.Results) != 1 {
		t.Fatalf("resolve denied knowledge: results=%+v err=%v", deniedKnowledge.Results, err)
	}
	allowedDocumentID := allowedKnowledge.Results[0].Result.DocumentID
	deniedChunkID := deniedKnowledge.Results[0].Result.ChunkID

	policy, err := authz.NewClaimsPolicy([]authz.Grant{
		{
			Permissions: []string{"capability.reader"},
			Actions: []authz.Action{
				authz.ActionCapabilitySearch,
				authz.ActionCapabilityDescribe,
				authz.ActionCapabilityMaterialize,
			},
			Resources: []authz.ResourceRule{{Namespace: "team", Repository: "repo-a"}},
		},
		{
			Permissions: []string{"knowledge.reader"},
			Actions:     []authz.Action{authz.ActionKnowledgeSearch, authz.ActionKnowledgeRead},
			Resources:   []authz.ResourceRule{{IDs: []string{allowedDocumentID}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	identity := authn.Identity{
		Subject:        "claims-e2e-user",
		OrganizationID: "demo",
		Permissions: map[string]struct{}{
			"capability.reader": {},
			"knowledge.reader":  {},
		},
	}
	validator := fixedClaimsValidator{token: "claims-e2e-token", identity: identity}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String()
	app := httpserver.NewComplete(nil, nil, index, "demo", candidate.Signer{Key: []byte("claims-candidate-key")}, packages, packageurl.Signer{Key: []byte("claims-package-key")}, catalog, baseURL)
	app.ConfigureCapabilities(capabilities)
	app.ConfigureKnowledge(knowledgeService)
	app.ConfigureAuthorization(policy)
	t.Cleanup(func() {
		app.ConfigureAuthorization(nil)
		app.ConfigureCapabilities(nil)
		app.ConfigureKnowledge(nil)
	})
	httpSrv := &http.Server{Handler: app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "oidc", OrganizationID: "demo", Validator: validator})}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(listener) }()
	t.Cleanup(func() {
		_ = httpSrv.Shutdown(context.Background())
		select {
		case err := <-serveErr:
			if err != nil && err != http.ErrServerClosed {
				t.Errorf("local claims server: %v", err)
			}
		default:
		}
	})

	client := adapter.Client{Server: baseURL + "/mcp", Token: validator.token}
	aScope := adapter.CapabilityScope{Namespace: "team", Repository: "repo-a"}
	allowedCapabilities, err := client.SearchCapabilities(ctx, "repository A deployment migration release", aScope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(allowedCapabilities.Candidates) != 1 || allowedCapabilities.Candidates[0].Capability.Source.RepositoryID != "local-a" {
		t.Fatalf("authorized capability results = %+v", allowedCapabilities.Candidates)
	}
	if _, err := client.SearchCapabilities(ctx, "repository B deployment migration release", adapter.CapabilityScope{Namespace: "team", Repository: "repo-b"}, 10); err == nil {
		t.Fatal("repository B capability search unexpectedly authorized")
	}

	candidate := allowedCapabilities.Candidates[0]
	if _, err := client.DescribeCapability(ctx, candidate.CandidateID, aScope); err != nil {
		t.Fatalf("authorized capability describe failed: %v", err)
	}
	materialized, entrypoint, err := client.Materialize(ctx, candidate.CandidateID, filepath.Join(root, "materialized"))
	if err != nil {
		t.Fatalf("authorized materialization/package download failed: %v", err)
	}
	if materialized.Lifecycle.RevisionID != candidate.Capability.Provenance.RevisionID {
		t.Fatalf("materialized revision = %q, want %q", materialized.Lifecycle.RevisionID, candidate.Capability.Provenance.RevisionID)
	}
	if _, err := os.Stat(entrypoint); err != nil {
		t.Fatalf("materialized entrypoint missing: %v", err)
	}

	httpClient := &http.Client{Transport: e2eBearerTransport{token: validator.token}}
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "claims-knowledge-e2e", Version: "1"}, nil)
	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: baseURL + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	knowledgeSearch := callKnowledgeSearch(t, ctx, session, "claims authorization knowledge", 10)
	if len(knowledgeSearch.Results) != 1 || knowledgeSearch.Results[0].Result.DocumentID != allowedDocumentID {
		t.Fatalf("claims-filtered knowledge results = %+v", knowledgeSearch.Results)
	}
	if read := callKnowledgeRead(t, ctx, session, knowledgeSearch.Results[0].Result.ChunkID); read.Chunk.DocumentID != allowedDocumentID {
		t.Fatalf("authorized knowledge read = %+v", read)
	}
	deniedRead, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "read_knowledge", Arguments: map[string]any{"chunk_id": deniedChunkID}})
	if err != nil {
		t.Fatal(err)
	}
	if !deniedRead.IsError {
		t.Fatal("unauthorized knowledge document was readable")
	}
}

type fixedClaimsValidator struct {
	token    string
	identity authn.Identity
}

func (v fixedClaimsValidator) Authenticate(authorization string) (authn.Identity, error) {
	if authorization != "Bearer "+v.token {
		return authn.Identity{}, authn.ErrUnauthorized
	}
	return v.identity, nil
}

type e2eBearerTransport struct {
	token string
}

func (t e2eBearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(request)
}

func writeClaimsKnowledgeFixture(t *testing.T, root, name, title, body string) {
	t.Helper()
	content := "---\ntype: Reference\ntitle: " + title + "\nstatus: stable\n---\n\n# " + title + "\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
