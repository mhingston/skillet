package e2e

import (
	"context"
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

	authn "github.com/mhingston/skillet/internal/auth"
	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
)

type collaborationBrowserValidator struct{}

func (collaborationBrowserValidator) Authenticate(authorization string) (authn.Identity, error) {
	if authorization != "Bearer browser-token" {
		return authn.Identity{}, authn.ErrUnauthorized
	}
	return authn.Identity{
		Subject:        "collaboration-browser",
		OrganizationID: "demo",
		Permissions:    map[string]struct{}{"capability.reader": {}},
	}, nil
}

func TestM36CollaborationBrowserWorkflow(t *testing.T) {
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

	repoRoot := filepath.Join(root, "central")
	writeGovernanceSkill(t, repoRoot, "release", "production release verification collaboration workflow", nil)
	writeGovernanceSkill(t, repoRoot, "incident", "incident response triage workflow", nil)
	result := syncM1Repository(t, ctx, repoRoot, "central", catalog, packages)
	if result.Admitted != 2 || result.Quarantined != 0 {
		t.Fatalf("admission = %+v", result)
	}
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
	capabilities, err := capability.New(index, []capability.SourcePolicy{{
		RepositoryID: "central",
		Scope:        mustCapabilityScope(t, "demo", "", ""),
		Owner:        "platform-team",
		Maintainers:  []string{"collaboration-browser"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	var release, incident search.Document
	for _, doc := range docs {
		switch doc.SkillID {
		case "demo/central/release":
			release = doc
		case "demo/central/incident":
			incident = doc
		}
	}
	if release.ID == "" || incident.ID == "" {
		t.Fatalf("fixture revisions missing: release=%q incident=%q", release.ID, incident.ID)
	}

	scope := mustCapabilityScope(t, "demo", "", "")
	before, _, err := capabilities.Search("production release verification", 50, 50, 10, 60, scope, search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	beforeOrder := candidateOrder(before)

	app := httpserver.NewComplete(nil, nil, index, "demo", candidate.Signer{Key: []byte("collaboration-candidate-key")}, packages, packageurl.Signer{Key: []byte("collaboration-package-key")}, catalog, "http://example.invalid")
	app.ConfigureCapabilities(capabilities)
	policy, err := authz.NewClaimsPolicy([]authz.Grant{
		{
			Permissions: []string{"capability.reader"},
			Actions:     []authz.Action{authz.ActionCapabilityDescribe},
			Resources:   []authz.ResourceRule{{}},
		},
		{
			Permissions: []string{"capability.reader"},
			Actions: []authz.Action{
				authz.ActionCollaborationRead,
				authz.ActionCollaborationComment,
				authz.ActionCollaborationWatch,
			},
			Resources: []authz.ResourceRule{{IDs: []string{"demo/central/release"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	app.ConfigureAuthorization(policy)
	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{
		Mode: "static", OrganizationID: "demo", Validator: collaborationBrowserValidator{},
	}))
	defer server.Close()

	client := server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	get := func(path, token string) (int, string) {
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
		return resp.StatusCode, string(body)
	}
	post := func(path, token string, form url.Values) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
		return resp.StatusCode, string(body)
	}

	collaborationPath := "/ui/catalogue/" + url.PathEscape(release.ID) + "/collaboration"
	if status, _ := get(collaborationPath, ""); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated collaboration status=%d, want 401", status)
	}
	hiddenPath := "/ui/catalogue/" + url.PathEscape(incident.ID) + "/collaboration"
	if status, hidden := get(hiddenPath, "browser-token"); status != http.StatusNotFound || strings.Contains(hidden, "incident response") {
		t.Fatalf("unauthorised thread status=%d leaked=%q", status, hidden)
	}
	status, body := get(collaborationPath, "browser-token")
	if status != http.StatusOK {
		t.Fatalf("collaboration status=%d body=%s", status, body)
	}
	for _, want := range []string{"Capability collaboration", release.ID, "No universal quality score", "materialisation_prepared", "@mentions are intentionally disabled"} {
		if !strings.Contains(body, want) {
			t.Fatalf("collaboration page missing %q: %s", want, body)
		}
	}
	csrf := extractCollaborationCSRF(t, body)

	status, _ = post(collaborationPath+"/comments", "browser-token", url.Values{"_csrf": {"invalid"}, "body": {"blocked"}})
	if status != http.StatusForbidden {
		t.Fatalf("invalid csrf status=%d, want 403", status)
	}

	status, _ = post(collaborationPath+"/watch", "browser-token", url.Values{"_csrf": {csrf}, "mode": {"watch"}})
	if status != http.StatusSeeOther {
		t.Fatalf("watch status=%d, want 303", status)
	}

	malicious := `<script>alert("collaboration-xss")</script> **not executable markdown**`
	status, _ = post(collaborationPath+"/comments", "browser-token", url.Values{"_csrf": {csrf}, "body": {malicious}, "bind_revision": {"1"}})
	if status != http.StatusSeeOther {
		t.Fatalf("post comment status=%d, want 303", status)
	}
	status, body = get(collaborationPath, "browser-token")
	if status != http.StatusOK {
		t.Fatalf("thread status=%d body=%s", status, body)
	}
	if strings.Contains(body, malicious) || !strings.Contains(body, html.EscapeString(malicious)) {
		t.Fatalf("comment was not rendered inert: %s", body)
	}
	if !strings.Contains(body, release.ID) {
		t.Fatalf("immutable revision context missing: %s", body)
	}

	status, activity := get("/ui/activity", "browser-token")
	if status != http.StatusOK || !strings.Contains(activity, "release") || !strings.Contains(activity, html.EscapeString(malicious)) {
		t.Fatalf("activity status=%d body=%s", status, activity)
	}

	moderate := regexp.MustCompile(`/collaboration/comments/([0-9]+)/moderate`).FindStringSubmatch(body)
	if len(moderate) != 2 {
		t.Fatalf("moderation action missing: %s", body)
	}
	status, _ = post(collaborationPath+"/comments/"+moderate[1]+"/moderate", "browser-token", url.Values{"_csrf": {csrf}, "reason": {"unsafe example"}})
	if status != http.StatusSeeOther {
		t.Fatalf("moderation status=%d, want 303", status)
	}
	status, body = get(collaborationPath, "browser-token")
	if status != http.StatusOK || !strings.Contains(body, "[comment moderated]") || strings.Contains(body, "collaboration-xss") {
		t.Fatalf("moderated thread status=%d body=%s", status, body)
	}
	var auditDetails string
	if err := db.QueryRowContext(ctx, `SELECT details_json FROM audit_events WHERE organization_id='demo' AND event_type='capability_comment_moderated' ORDER BY id DESC LIMIT 1`).Scan(&auditDetails); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditDetails, malicious) || !strings.Contains(auditDetails, "body_sha256") {
		t.Fatalf("moderation audit leaked removed content or lost digest: %s", auditDetails)
	}

	after, _, err := capabilities.Search("production release verification", 50, 50, 10, 60, scope, search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateOrder(after); fmt.Sprint(got) != fmt.Sprint(beforeOrder) {
		t.Fatalf("collaboration changed ranking order: before=%v after=%v", beforeOrder, got)
	}
}

func extractCollaborationCSRF(t *testing.T, body string) string {
	t.Helper()
	match := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 || match[1] == "" {
		t.Fatalf("csrf token missing from collaboration page: %s", body)
	}
	return html.UnescapeString(match[1])
}

func candidateOrder(values []capability.Candidate) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, fmt.Sprintf("%d:%s@%s", value.Ranking.Rank, value.Capability.Identity.ID, value.Capability.Provenance.RevisionID))
	}
	return out
}
