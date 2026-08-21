package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/packagebuilder"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHealthReadinessAndMetrics(t *testing.T) {
	r := &Readiness{}
	h := New(slog.New(slog.NewTextHandler(io.Discard, nil)), r).Handler("/mcp", 1<<20)
	for _, path := range []string{"/healthz", "/metrics"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready: %d", rr.Code)
	}
	r.SQLite.Store(true)
	r.PackageStore.Store(true)
	r.LexicalIndex.Store(true)
	r.InitialSync.Store(true)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("ready: %d", rr.Code)
	}
}

func TestMCPHandlerIsMounted(t *testing.T) {
	h := New(nil, nil).Handler("/mcp", 1<<20, AuthConfig{Mode: "development", OrganizationID: "demo"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code == http.StatusNotFound {
		t.Fatal("MCP endpoint is not mounted")
	}
}

func TestHandlerDoesNotSilentlyDisableAuthentication(t *testing.T) {
	h := New(nil, nil).Handler("/mcp", 1<<20)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`)))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s; missing auth configuration must fail closed", rr.Code, rr.Body.String())
	}
}

func TestMetricsExposeOperationalCounters(t *testing.T) {
	h := New(nil, nil).Handler("/mcp", 1<<20, AuthConfig{Mode: "static", StaticToken: "secret", OrganizationID: "demo"})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wrong")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "skillet_auth_failures_total 1") {
		t.Fatalf("metrics = %s", rr.Body.String())
	}
}

func TestRequestIDIsReturnedAndPropagatedToContext(t *testing.T) {
	h := New(nil, nil).Handler("/mcp", 1<<20, AuthConfig{Mode: "development", OrganizationID: "demo"})
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Header.Set("X-Request-ID", "req_test_123")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if got := rr.Header().Get("X-Request-ID"); got != "req_test_123" {
		t.Fatalf("request id = %q", got)
	}
	generated := httptest.NewRecorder()
	h.ServeHTTP(generated, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if got := generated.Header().Get("X-Request-ID"); !strings.HasPrefix(got, "req_") || got == "req_" {
		t.Fatalf("generated request id = %q", got)
	}
}

func TestMetricsExposeObservedSyncValues(t *testing.T) {
	s := New(nil, nil)
	s.ObserveRepositorySync(250*time.Millisecond, true)
	s.ObserveEmbeddingRequest()
	s.ObserveRerankerRequest()
	rr := httptest.NewRecorder()
	s.Handler("/mcp", 1<<20, AuthConfig{Mode: "development", OrganizationID: "demo"}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	for _, want := range []string{
		"skillet_repository_sync_duration_seconds 0.250000000",
		"skillet_repository_sync_duration_seconds_total 0.250000000",
		"skillet_repository_last_success_timestamp ",
		"skillet_embedding_requests_total 1",
		"skillet_rerank_requests_total 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestAuditFailureIsCountedAndRequestIDIsIncluded(t *testing.T) {
	s := New(nil, nil)
	s.catalogue = &catalogue.Store{}
	ctx := context.WithValue(context.Background(), requestIDContextKey{}, "req_audit_test")
	err := s.recordAudit(ctx, "demo", "search_executed", map[string]any{})
	if err == nil || s.metrics.AuditFailures.Load() != 1 {
		t.Fatalf("audit error = %v, failures = %d", err, s.metrics.AuditFailures.Load())
	}
	// recordAudit must not mutate an audit payload with an unbounded query or
	// secret; the request correlation field is the only transport context field.
	details := map[string]any{}
	_ = s.recordAudit(ctx, "demo", "search_executed", details)
	if details["request_id"] != "req_audit_test" {
		t.Fatalf("details = %#v", details)
	}
}

func TestOfficialMCPClientListsSkilletTools(t *testing.T) {
	ts := httptest.NewServer(New(nil, nil).Handler("/mcp", 1<<20, AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer ts.Close()
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range tools.Tools {
		seen[tool.Name] = true
	}
	if !seen["search_skills"] || !seen["list_skills"] || !seen["materialize_skill"] {
		t.Fatalf("tools = %+v", tools.Tools)
	}
}

func TestListSkillsReturnsDeterministicPaginatedMetadata(t *testing.T) {
	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range []search.Document{
		{ID: "demo/skills/z", SkillID: "zeta", OrganizationID: "demo", Name: "zeta", Description: "Z", Searchable: true},
		{ID: "demo/skills/a", SkillID: "alpha", OrganizationID: "demo", Name: "alpha", Description: "A", Searchable: true},
		{ID: "other/skills/x", OrganizationID: "other", Name: "other", Description: "Other", Searchable: true},
	} {
		if err := index.Add(doc); err != nil {
			t.Fatal(err)
		}
	}
	s := NewWithSearch(nil, nil, index, "demo", candidate.Signer{Key: []byte("candidate-key")})
	_, out, err := s.listSkillsTool(context.Background(), nil, listSkillsInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 2 || len(out.Skills) != 1 || out.Skills[0].Name != "alpha" || out.Skills[0].SkillID != "alpha" || !out.HasMore {
		t.Fatalf("list output = %+v", out)
	}
	_, page, err := s.listSkillsTool(context.Background(), nil, listSkillsInput{Limit: 1, Offset: 1})
	if err != nil || len(page.Skills) != 1 || page.Skills[0].Name != "zeta" || page.HasMore {
		t.Fatalf("second page = %+v, err=%v", page, err)
	}
}

func TestMCPListSkillsReturnsStructuredMetadata(t *testing.T) {
	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Add(search.Document{ID: "demo/skills/plan", OrganizationID: "demo", Name: "plan", Description: "Make plans", Searchable: true}); err != nil {
		t.Fatal(err)
	}
	s := NewWithSearch(nil, nil, index, "demo", candidate.Signer{Key: []byte("candidate-key")})
	ts := httptest.NewServer(s.Handler("/mcp", 1<<20, AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer ts.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "list-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_skills", Arguments: map[string]any{"limit": 1}})
	if err != nil || result.IsError {
		t.Fatalf("list result error=%v result=%+v", err, result)
	}
	var output listSkillsOutput
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if output.Total != 1 || len(output.Skills) != 1 || output.Skills[0].ID != "demo/skills/plan" {
		t.Fatalf("structured list output = %s", encoded)
	}
}

func TestSearchToolAdvertisesConfiguredMaximum(t *testing.T) {
	ts := httptest.NewServer(New(nil, nil).Handler("/mcp", 1<<20, AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer ts.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "search_skills" {
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(schema), `"maximum":10`) {
				t.Fatalf("search schema does not advertise max 10: %s", schema)
			}
			return
		}
	}
	t.Fatal("search_skills tool not found")
}

func TestSearchUsesAuthenticatedOrganizationForCandidates(t *testing.T) {
	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Add(search.Document{ID: "tenant-b/repo/plan", Name: "plan", Description: "make a plan", Searchable: true}); err != nil {
		t.Fatal(err)
	}
	s := NewWithSearch(nil, nil, index, "tenant-a", candidate.Signer{Key: []byte("candidate-key")})
	ctx := context.WithValue(context.Background(), organizationContextKey{}, "tenant-b")
	_, output, err := s.searchTool(ctx, nil, searchInput{Query: "plan", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Candidates) != 1 {
		t.Fatalf("candidates = %+v", output.Candidates)
	}
	if _, err := s.signer.Verify(output.Candidates[0].CandidateID, "tenant-b", time.Now()); err != nil {
		t.Fatalf("candidate was not bound to authenticated organization: %v", err)
	}
	if _, err := s.signer.Verify(output.Candidates[0].CandidateID, "tenant-a", time.Now()); err == nil {
		t.Fatal("candidate remained authorized for configured organization")
	}
}

func TestMaterializeUsesAuthenticatedOrganizationForCatalogueAndPackage(t *testing.T) {
	s, first, _ := lockedMaterializeFixture(t)
	s.organizationID = "tenant-a"
	token, err := s.signer.Sign(candidate.Payload{Version: 1, OrganizationID: "demo", RevisionID: first.RevisionID, QueryID: "query", IssuedAt: time.Now().Add(-time.Second).Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), organizationContextKey{}, "demo")
	_, output, err := s.materializeTool(ctx, nil, materializeInput{CandidateID: token})
	if err != nil {
		t.Fatal(err)
	}
	if output.Skill.ID != first.SkillID {
		t.Fatalf("skill = %+v, want %q", output.Skill, first.SkillID)
	}
	parsed, err := url.Parse(output.Package.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.packageSigner.Verify(parsed.Query().Get("token"), "demo", time.Now()); err != nil {
		t.Fatalf("package URL was not bound to authenticated organization: %v", err)
	}
	if _, err := s.packageSigner.Verify(parsed.Query().Get("token"), "tenant-a", time.Now()); err == nil {
		t.Fatal("package URL remained authorized for configured organization")
	}
}

func TestMaterializeToolLockedModeUsesExactHistoricalRevision(t *testing.T) {
	s, first, _ := lockedMaterializeFixture(t)
	input := materializeInput{Locked: &lockedInput{
		SkillID:       first.SkillID,
		RepositoryID:  first.RepositoryID,
		Path:          first.Path,
		Commit:        first.Commit,
		Tree:          first.Tree,
		ArchiveSHA256: first.ArchiveSHA256TarGZ,
		Format:        "tar.gz",
	}}
	_, out, err := s.materializeTool(context.Background(), nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if out.Resolved.Commit != first.Commit || out.Resolved.Tree != first.Tree || out.Package.ArchiveSHA256 != first.ArchiveSHA256TarGZ {
		t.Fatalf("locked materialization resolved wrong package: %+v", out)
	}
	if out.Lockfile.Entry["resolved"].(map[string]any)["commit"] != first.Commit {
		t.Fatalf("lock entry was not preserved: %+v", out.Lockfile.Entry)
	}
}

func TestMaterializeOmittedClientReturnsBothPackageVariants(t *testing.T) {
	s, first, _ := lockedMaterializeFixture(t)
	token, err := s.signer.Sign(candidate.Payload{Version: 1, OrganizationID: "demo", RevisionID: first.RevisionID, QueryID: "query", IssuedAt: time.Now().Add(-time.Second).Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	_, out, err := s.materializeTool(context.Background(), nil, materializeInput{CandidateID: token})
	if err != nil {
		t.Fatal(err)
	}
	if out.Materialization.Shell != "posix" || len(out.Variants) != 1 || out.Variants[0].Shell != "powershell" || out.Variants[0].Package.Format != "zip" {
		t.Fatalf("materialization variants = %+v", out)
	}
}

func TestMaterializeToolRequiresExactlyOneResolutionMode(t *testing.T) {
	s, first, _ := lockedMaterializeFixture(t)
	locked := &lockedInput{SkillID: first.SkillID, RepositoryID: first.RepositoryID, Path: first.Path, Commit: first.Commit, Tree: first.Tree, ArchiveSHA256: first.ArchiveSHA256TarGZ, Format: "tar.gz"}
	cases := []struct {
		name  string
		input materializeInput
	}{
		{name: "neither", input: materializeInput{}},
		{name: "both", input: materializeInput{CandidateID: "candidate", Locked: locked}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := s.materializeTool(context.Background(), nil, tc.input); err == nil {
				t.Fatal("expected exactly-one resolution error")
			}
		})
	}
}

func TestHTTPMCPMaterializeLockedModeRejectsDigestMismatch(t *testing.T) {
	s, first, _ := lockedMaterializeFixture(t)
	ts := httptest.NewServer(s.Handler("/mcp", 1<<20, AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer ts.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "locked-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "materialize_skill",
		Arguments: map[string]any{"locked": map[string]any{
			"skill_id": first.SkillID, "repository_id": first.RepositoryID, "path": first.Path,
			"commit": first.Commit, "tree": first.Tree, "archive_sha256": strings.Repeat("0", 64), "format": "tar.gz",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("digest mismatch unexpectedly succeeded: %+v", result)
	}
}

func TestRemoteMCPSearchMaterializeExecuteAndRestore(t *testing.T) {
	s, first, _ := lockedMaterializeFixture(t)
	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Add(search.Document{ID: first.RevisionID, OrganizationID: "demo", RepositoryID: "skills", Path: first.Path, Commit: first.Commit, Tree: first.Tree, Name: first.Name, Description: "Create plans.", Searchable: true}); err != nil {
		t.Fatal(err)
	}
	s.search = index
	ts := httptest.NewServer(s.Handler("/mcp", 1<<20, AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer ts.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "journey-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	searchResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "search_skills", Arguments: map[string]any{"query": "create a plan", "limit": 1}})
	if err != nil || searchResult.IsError {
		t.Fatalf("search result error=%v result=%+v", err, searchResult)
	}
	searchJSON, err := json.Marshal(searchResult.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var searchOutput struct {
		Candidates []struct {
			CandidateID string `json:"candidate_id"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(searchJSON, &searchOutput); err != nil || len(searchOutput.Candidates) != 1 || searchOutput.Candidates[0].CandidateID == "" {
		t.Fatalf("search structured content = %s, err=%v", searchJSON, err)
	}

	materialized, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "materialize_skill", Arguments: map[string]any{"candidate_id": searchOutput.Candidates[0].CandidateID, "client": map[string]any{"os": "linux", "shell": "posix"}}})
	if err != nil || materialized.IsError {
		t.Fatalf("materialize result error=%v result=%+v", err, materialized)
	}
	materializeJSON, err := json.Marshal(materialized.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var materializeOutput struct {
		Package struct {
			ArchiveSHA256 string `json:"archive_sha256"`
		} `json:"package"`
		Materialization struct {
			Command string `json:"command"`
		} `json:"materialization"`
	}
	if err := json.Unmarshal(materializeJSON, &materializeOutput); err != nil || materializeOutput.Package.ArchiveSHA256 == "" || materializeOutput.Materialization.Command == "" {
		t.Fatalf("materialize structured content = %s, err=%v", materializeJSON, err)
	}
	cache := t.TempDir()
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	materializationCommand := strings.ReplaceAll(materializeOutput.Materialization.Command, "https://skillet.example", ts.URL)
	command := exec.Command("sh", "-c", materializationCommand)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("first acquisition failed: %v\n%s", err, output)
	}
	entrypoint := filepath.Join(cache, "skillet", "content", "sha256", materializeOutput.Package.ArchiveSHA256, "plan", "SKILL.md")
	if _, err := os.Stat(entrypoint); err != nil {
		t.Fatalf("materialized entrypoint missing: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(cache, "skillet")); err != nil {
		t.Fatal(err)
	}

	locked, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "materialize_skill", Arguments: map[string]any{"locked": map[string]any{
		"skill_id": first.SkillID, "repository_id": "skills", "path": first.Path, "commit": first.Commit, "tree": first.Tree, "archive_sha256": first.ArchiveSHA256TarGZ, "format": "tar.gz",
	}, "client": map[string]any{"os": "linux", "shell": "posix"}}})
	if err != nil || locked.IsError {
		t.Fatalf("locked materialize result error=%v result=%+v", err, locked)
	}
	lockedJSON, err := json.Marshal(locked.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var lockedOutput struct {
		Package struct {
			ArchiveSHA256 string `json:"archive_sha256"`
		} `json:"package"`
		Materialization struct {
			Command string `json:"command"`
		} `json:"materialization"`
	}
	if err := json.Unmarshal(lockedJSON, &lockedOutput); err != nil || lockedOutput.Package.ArchiveSHA256 != first.ArchiveSHA256TarGZ {
		t.Fatalf("locked output = %s, err=%v", lockedJSON, err)
	}
	command = exec.Command("sh", "-c", strings.ReplaceAll(lockedOutput.Materialization.Command, "https://skillet.example", ts.URL))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("locked acquisition failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(entrypoint); err != nil {
		t.Fatalf("restored entrypoint missing: %v", err)
	}
}

func lockedMaterializeFixture(t *testing.T) (*Server, catalogue.RevisionInfo, catalogue.RevisionInfo) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	catalog := catalogue.New(db, packages)
	admit := func(commit, tree string, content []byte) catalogue.RevisionInfo {
		archive, err := packagebuilder.Build("plan", "plan", []packagebuilder.Entry{{Path: "plan/SKILL.md", Kind: packagebuilder.Regular, Mode: 0644}}, func(string) ([]byte, error) {
			return append([]byte("---\nname: plan\ndescription: Create plans.\n---\n"), content...), nil
		}, packagebuilder.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		tar, err := packages.Put("tar.gz", archive.TarGZ)
		if err != nil {
			t.Fatal(err)
		}
		zip, err := packages.Put("zip", archive.ZIP)
		if err != nil {
			t.Fatal(err)
		}
		_, err = catalog.Admit(context.Background(), catalogue.Repository{ID: "skills", OrganizationID: "demo", URL: "https://example/skills.git", Ref: "refs/heads/main", TrustLevel: "approved", Owner: "team"}, discovery.Skill{RelativePath: "plan", State: discovery.Admitted, Searchable: true, Frontmatter: validFrontmatter()}, commit, tree, catalogue.PackageDigests{TarGZ: tar, ZIP: zip})
		if err != nil {
			t.Fatal(err)
		}
		info, err := catalog.Revision(context.Background(), "demo", catalogue.RevisionID("demo", "skills", "plan", commit, tree))
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	first := admit("commit-one", "tree-one", []byte("first"))
	second := admit("commit-two", "tree-two", []byte("second"))
	s := NewComplete(nil, nil, nil, "demo", candidate.Signer{Key: []byte("candidate")}, packages, packageurl.Signer{Key: []byte("package")}, catalog, "https://skillet.example")
	return s, first, second
}

func validFrontmatter() skillspec.Frontmatter {
	return skillspec.Frontmatter{Name: "plan", Description: "Create plans."}
}

func TestAcquisitionCommandsValidateReceiptAndEntrypoint(t *testing.T) {
	digest := strings.Repeat("a", 64)
	posix := posixCommand("https://example.test/pkg", digest, "/tmp/cache/"+digest, "plan", "org/repo/plan", "commit-1")
	for _, want := range []string{"archiveSha256", "skillId", "commit", "SKILL.md", "sha256sum", "shasum", "tar -xzf"} {
		if !strings.Contains(posix, want) {
			t.Errorf("posix command missing %q: %s", want, posix)
		}
	}
	if strings.Contains(posix, "jq") {
		t.Error("posix command must not require jq")
	}
	if strings.Contains(posix, "rm -rf") {
		t.Error("posix command must not use rm -rf; host safety wrappers commonly block it")
	}
	powershell := powershellCommand("https://example.test/pkg", digest, `$env:LOCALAPPDATA\Skillet\Cache\`+digest, "plan", "org/repo/plan", "commit-1")
	for _, want := range []string{"ConvertFrom-Json", "Get-FileHash", "Expand-Archive", "SKILL.md", "archiveSha256"} {
		if !strings.Contains(powershell, want) {
			t.Errorf("powershell command missing %q: %s", want, powershell)
		}
	}
}

func TestPOSIXAcquisitionCommandExecutesAndIsIdempotent(t *testing.T) {
	packageData, err := packagebuilder.Build("plan", "plan", []packagebuilder.Entry{{Path: "plan/SKILL.md", Kind: packagebuilder.Regular, Mode: 0644}}, func(string) ([]byte, error) {
		return []byte("---\nname: plan\ndescription: test\n---\n"), nil
	}, packagebuilder.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(packageData.TarGZ)))
		_, _ = w.Write(packageData.TarGZ)
	}))
	defer server.Close()
	digestBytes := sha256.Sum256(packageData.TarGZ)
	digest := hex.EncodeToString(digestBytes[:])
	root := t.TempDir()
	cache := filepath.Join(root, "cache", digest)
	cmdText := posixCommand(server.URL+"/package.tar.gz", digest, cache, "plan", "demo/repo/plan", "commit")
	run := func() string {
		cmd := exec.Command("sh", "-c", cmdText)
		cmd.Dir = root
		output, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("acquisition command failed: %v\n%s", runErr, output)
		}
		return strings.TrimSpace(string(output))
	}
	entrypoint := run()
	if _, err := os.Stat(entrypoint); err != nil {
		t.Fatalf("entrypoint missing: %v", err)
	}
	if got := run(); got != entrypoint {
		t.Fatalf("idempotent output = %q, want %q", got, entrypoint)
	}
	if strings.HasPrefix(entrypoint, filepath.Join(root, ".git")) {
		t.Fatal("materialized package was placed in the repository")
	}
}
