package e2e

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/knowledge"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
)

const htmxV2010BlobSHA = "3b7ac1aceb211ca716c7a9c5774c649f74331ee1"

func TestOfflineM3BrowserAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	root := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)

	centralRoot := filepath.Join(root, "central")
	writeGovernanceSkill(t, centralRoot, "release", "production release verification rollout central skill <script>alert(1)</script> browser-escape-marker", nil)
	writeGovernanceSkill(t, centralRoot, "legacy-release", "legacy release workflow retained for explicit selection", map[string]string{
		governance.DeprecatedKey: "true",
		governance.ReplacedByKey: "demo/central/release",
	})
	writeGovernanceSkill(t, centralRoot, "withdrawn-release", "withdrawn unsafe release workflow", map[string]string{
		governance.StateKey:  string(capability.StatusYanked),
		governance.ReasonKey: "fixture withdrawal",
	})
	centralResult := syncM1Repository(t, ctx, centralRoot, "central", catalog, packages)
	if centralResult.Admitted != 3 || centralResult.Quarantined != 0 {
		t.Fatalf("central admission = %+v, want 3 admitted", centralResult)
	}
	syncCapabilityFixture(t, ctx, root, "local-a", "release-a", "repository A deployment migration release verification local skill", catalog, packages)
	syncCapabilityFixture(t, ctx, root, "local-b", "release-b", "repository B deployment migration release verification local skill", catalog, packages)

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
		{RepositoryID: "central", Scope: mustCapabilityScope(t, "demo", "", ""), Owner: "platform-team"},
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
	bundleRoot := filepath.Join(root, "knowledge")
	copyFixtureTree(t, filepath.Join("..", "knowledge", "testdata", "okf-v02"), bundleRoot)
	if _, err := knowledgeService.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: "org-knowledge", Root: bundleRoot, Locator: "git://fixture/org-knowledge", Revision: "browser-v1"}}); err != nil {
		t.Fatal(err)
	}

	app := httpserver.NewComplete(nil, nil, index, "demo", candidate.Signer{Key: []byte("browser-candidate-key")}, packages, packageurl.Signer{Key: []byte("browser-package-key")}, catalog, "http://example.invalid")
	app.ConfigureCapabilities(capabilities)
	app.ConfigureKnowledge(knowledgeService)
	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "static", StaticToken: "browser-token", OrganizationID: "demo"}))
	defer server.Close()

	client := server.Client()
	get := func(t *testing.T, path, token string) (int, http.Header, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, resp.Header, string(body)
	}

	t.Run("authenticated_shell_and_security", func(t *testing.T) {
		status, headers, body := get(t, "/ui/catalogue", "browser-token")
		if status != http.StatusOK {
			t.Fatalf("catalogue status = %d, body=%s", status, body)
		}
		for _, want := range []string{"Find reusable capabilities", "Skip to content", "static", "authenticated", `role="separator"`, `tabindex="0"`, `"allowEval":false`} {
			if !strings.Contains(body, want) {
				t.Fatalf("catalogue shell missing %q", want)
			}
		}
		csp := headers.Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, want) {
				t.Fatalf("CSP %q missing %q", csp, want)
			}
		}
		status, _, _ = get(t, "/ui/catalogue", "")
		if status != http.StatusUnauthorized {
			t.Fatalf("unauthenticated catalogue status = %d, want 401", status)
		}
	})

	var localADoc, centralDoc, deprecatedDoc, yankedDoc search.Document
	for _, doc := range docs {
		switch {
		case doc.RepositoryID == "local-a":
			localADoc = doc
		case doc.SkillID == "demo/central/release":
			centralDoc = doc
		case doc.SkillID == "demo/central/legacy-release":
			deprecatedDoc = doc
		case doc.SkillID == "demo/central/withdrawn-release":
			yankedDoc = doc
		}
	}
	for label, doc := range map[string]search.Document{"local-a": localADoc, "central": centralDoc, "deprecated": deprecatedDoc, "yanked": yankedDoc} {
		if doc.ID == "" {
			t.Fatalf("%s fixture revision missing from routing snapshot", label)
		}
	}

	t.Run("scoped_search_detail_and_materialisation", func(t *testing.T) {
		query := url.Values{"q": {"repository A deployment migration release verification"}, "namespace": {"team"}, "repository": {"repo-a"}}
		status, _, body := get(t, "/ui/catalogue?"+query.Encode(), "browser-token")
		if status != http.StatusOK {
			t.Fatalf("scoped search status=%d body=%s", status, body)
		}
		if !strings.Contains(body, "local-a") || !strings.Contains(body, "central") || strings.Contains(body, "local-b") {
			t.Fatalf("scoped catalogue visibility incorrect: %s", body)
		}
		pattern := regexp.MustCompile(`href="([^"]*/ui/catalogue/` + regexp.QuoteMeta(url.PathEscape(localADoc.ID)) + `[^\"]*)"`)
		match := pattern.FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatalf("local-a detail link not found for revision %s", localADoc.ID)
		}
		detailPath := html.UnescapeString(match[1])
		if strings.HasPrefix(detailPath, server.URL) {
			detailPath = strings.TrimPrefix(detailPath, server.URL)
		}
		status, _, detail := get(t, detailPath, "browser-token")
		if status != http.StatusOK {
			t.Fatalf("detail status=%d body=%s", status, detail)
		}
		for _, want := range []string{localADoc.ID, "materialize_skill candidate_id=", "This browser never executes capabilities", "tar.gz SHA-256"} {
			if !strings.Contains(detail, want) {
				t.Fatalf("detail missing %q: %s", want, detail)
			}
		}

		crossScope := "/ui/catalogue/" + url.PathEscape(localADoc.ID) + "?namespace=team&repository=repo-b"
		status, _, _ = get(t, crossScope, "browser-token")
		if status != http.StatusNotFound && status != http.StatusForbidden {
			t.Fatalf("cross-scope direct detail status=%d, want 404/403", status)
		}
		status, _, _ = get(t, crossScope, "")
		if status != http.StatusUnauthorized {
			t.Fatalf("unauthenticated direct detail status=%d, want 401", status)
		}
	})

	t.Run("central_scope_and_escaped_untrusted_content", func(t *testing.T) {
		status, _, body := get(t, "/ui/catalogue?q="+url.QueryEscape("production release verification rollout central skill"), "browser-token")
		if status != http.StatusOK {
			t.Fatalf("central search status=%d body=%s", status, body)
		}
		if !strings.Contains(body, "central") || strings.Contains(body, "local-a") || strings.Contains(body, "local-b") {
			t.Fatalf("global catalogue leaked local capabilities: %s", body)
		}
		if strings.Contains(body, "<script>alert(1)</script>") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
			t.Fatalf("untrusted capability description was not rendered inert: %s", body)
		}
	})

	t.Run("governance_without_substitution", func(t *testing.T) {
		status, _, deprecated := get(t, "/ui/catalogue/"+url.PathEscape(deprecatedDoc.ID), "browser-token")
		if status != http.StatusOK {
			t.Fatalf("deprecated detail status=%d body=%s", status, deprecated)
		}
		for _, want := range []string{deprecatedDoc.ID, "deprecated", "demo/central/release", "guidance only; not followed automatically"} {
			if !strings.Contains(deprecated, want) {
				t.Fatalf("deprecated detail missing %q: %s", want, deprecated)
			}
		}
		if strings.Contains(deprecated, centralDoc.ID) && centralDoc.ID != deprecatedDoc.ID {
			t.Fatalf("deprecated detail silently substituted active revision %s", centralDoc.ID)
		}

		status, _, yanked := get(t, "/ui/catalogue/"+url.PathEscape(yankedDoc.ID), "browser-token")
		if status != http.StatusOK {
			t.Fatalf("yanked immutable detail status=%d body=%s", status, yanked)
		}
		for _, want := range []string{yankedDoc.ID, "yanked", "fixture withdrawal"} {
			if !strings.Contains(yanked, want) {
				t.Fatalf("yanked detail missing %q: %s", want, yanked)
			}
		}
		status, _, searchBody := get(t, "/ui/catalogue?q="+url.QueryEscape("withdrawn unsafe release workflow"), "browser-token")
		if status != http.StatusOK {
			t.Fatalf("yanked discovery status=%d", status)
		}
		if strings.Contains(searchBody, yankedDoc.ID) || strings.Contains(searchBody, "withdrawn-release") {
			t.Fatalf("yanked revision appeared in new discovery: %s", searchBody)
		}
	})

	t.Run("knowledge_search_read_and_backlinks", func(t *testing.T) {
		status, _, body := get(t, "/ui/knowledge?q="+url.QueryEscape("renewal objections"), "browser-token")
		if status != http.StatusOK {
			t.Fatalf("knowledge search status=%d body=%s", status, body)
		}
		if !strings.Contains(body, "Renewal objection policy") || !strings.Contains(body, "browser-v1") {
			t.Fatalf("knowledge search missing source-backed result: %s", body)
		}
		pattern := regexp.MustCompile(`href="(/ui/knowledge/[^"]+)"`)
		match := pattern.FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatalf("knowledge read link missing: %s", body)
		}
		status, _, read := get(t, html.UnescapeString(match[1]), "browser-token")
		if status != http.StatusOK {
			t.Fatalf("knowledge read status=%d body=%s", status, read)
		}
		for _, want := range []string{"Renewal objection policy", "browser-v1", "Backlinks"} {
			if !strings.Contains(read, want) {
				t.Fatalf("knowledge read missing %q: %s", want, read)
			}
		}
		if strings.Contains(read, "No authorised explicit backlinks.") {
			t.Fatalf("expected at least one explicit authorised backlink: %s", read)
		}
	})

	t.Run("responsive_and_keyboard_critical_path", func(t *testing.T) {
		status, _, css := get(t, "/ui/assets/skillet.css", "browser-token")
		if status != http.StatusOK {
			t.Fatalf("css status=%d", status)
		}
		for _, want := range []string{"@media (max-width: 720px)", "body.nav-open .sidebar", ".nav-toggle", ":focus-visible"} {
			if !strings.Contains(css, want) {
				t.Fatalf("responsive CSS missing %q", want)
			}
		}
		status, _, js := get(t, "/ui/assets/skillet.js", "browser-token")
		if status != http.StatusOK {
			t.Fatalf("js status=%d", status)
		}
		for _, want := range []string{"Escape", "ArrowLeft", "ArrowRight", "Home", "End", "data-sidebar-resize"} {
			if !strings.Contains(js, want) {
				t.Fatalf("keyboard JS missing %q", want)
			}
		}
		status, _, htmx := get(t, "/ui/assets/htmx.min.js", "browser-token")
		if status != http.StatusOK {
			t.Fatalf("htmx asset status=%d", status)
		}
		if got := gitBlobSHA([]byte(htmx)); got != htmxV2010BlobSHA {
			t.Fatalf("vendored htmx does not match upstream v2.0.10: blob=%s want=%s", got, htmxV2010BlobSHA)
		}
	})
}

func TestM3BrowserAssetsRemainLocalAndPinned(t *testing.T) {
	for _, path := range []string{"/ui/assets/htmx.min.js", "/ui/assets/skillet.css", "/ui/assets/skillet.js"} {
		if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
			t.Fatalf("browser asset unexpectedly external: %s", path)
		}
	}
}

// gitBlobSHA verifies exact vendored bytes against the content-addressed Git
// object from the upstream htmx v2.0.10 tag. SHA-1 here is Git object identity,
// not a cryptographic integrity primitive.
func gitBlobSHA(data []byte) string {
	h := sha1.New()
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(data))
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
