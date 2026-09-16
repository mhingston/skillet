package e2e

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/auditexport"
	authn "github.com/mhingston/skillet/internal/auth"
	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
)

type operatorTestValidator struct{}

func (operatorTestValidator) Authenticate(authorization string) (authn.Identity, error) {
	identity := func(subject, organization string, permissions ...string) authn.Identity {
		set := make(map[string]struct{}, len(permissions))
		for _, permission := range permissions {
			set[permission] = struct{}{}
		}
		return authn.Identity{Subject: subject, OrganizationID: organization, Permissions: set, Attributes: map[string][]string{}, Scopes: map[string]struct{}{}}
	}
	switch authorization {
	case "Bearer operator-token":
		return identity("operator", "demo", "operator.reader", "operator.prober"), nil
	case "Bearer user-token":
		return identity("ordinary-user", "demo", "capability.reader"), nil
	case "Bearer other-token":
		return identity("other-operator", "other", "operator.reader", "operator.prober"), nil
	default:
		return authn.Identity{}, authn.ErrUnauthorized
	}
}

type operatorFailingAuditSink struct{}

func (operatorFailingAuditSink) Export(context.Context, auditexport.Event) error {
	return errors.New("operator fixture sink unavailable")
}

func TestOfflineM3OperatorAcceptance(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	catalog := catalogue.New(db)

	seedOperatorRepository(t, ctx, catalog, "demo", "skills", "https://example.invalid/demo.git")
	seedOperatorRepository(t, ctx, catalog, "other", "private", "https://example.invalid/other.git")
	seedOperatorQuarantine(t, ctx, catalog, "demo", "skills", "bad-skill")
	seedOperatorQuarantine(t, ctx, catalog, "other", "private", "other-secret-skill")
	if err := catalog.RecordAudit(ctx, "demo", "repository_sync_succeeded", map[string]any{"repository_id": "demo/skills", "commit": "commit-bad-skill"}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.RecordAudit(ctx, "other", "repository_sync_failed", map[string]any{"repository_id": "other/private"}); err != nil {
		t.Fatal(err)
	}

	exporter := auditexport.New(operatorFailingAuditSink{}, nil)
	catalog.ConfigureAuditExporter(exporter)
	policy, err := authz.NewClaimsPolicy([]authz.Grant{
		{Permissions: []string{"operator.reader"}, Actions: []authz.Action{authz.ActionOperatorRead}, Resources: []authz.ResourceRule{{}}},
		{Permissions: []string{"operator.prober"}, Actions: []authz.Action{authz.ActionOperatorAuditProbe}, Resources: []authz.ResourceRule{{}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	app := httpserver.NewComplete(nil, nil, nil, "demo", candidate.Signer{}, nil, packageurl.Signer{}, catalog, "")
	app.ConfigureAuthorization(policy)
	t.Cleanup(func() { app.ConfigureAuthorization(nil) })
	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "oidc", OrganizationID: "demo", Validator: operatorTestValidator{}}))
	defer server.Close()
	client := &http.Client{
		Transport: server.Client().Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	request := func(t *testing.T, method, path, token string, form url.Values) (int, http.Header, string) {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		contents, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, resp.Header, string(contents)
	}

	t.Run("authorised_operator_sees_bounded_state", func(t *testing.T) {
		status, headers, body := request(t, http.MethodGet, "/ui/operator", "operator-token", nil)
		if status != http.StatusOK {
			t.Fatalf("operator status=%d body=%s", status, body)
		}
		for _, want := range []string{"Operator workspace", "demo/skills", "bad-skill", "Current quarantine", "Current quarantined", "Historical quarantined revisions", "Source-owned · read-only", "Raw bearer tokens", "Audit export health"} {
			if !strings.Contains(body, want) {
				t.Fatalf("operator page missing %q: %s", want, body)
			}
		}
		for _, forbidden := range []string{"other/private", "other-secret-skill", `name="repository_url"`, `name="governance_state"`, "/ui/operator/yank"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("operator page unexpectedly contains %q", forbidden)
			}
		}
		if !strings.Contains(headers.Get("Content-Security-Policy"), "frame-ancestors 'none'") || headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("operator security headers missing: %#v", headers)
		}
	})

	t.Run("ordinary_user_cannot_discover_or_invoke_operator_actions", func(t *testing.T) {
		status, _, body := request(t, http.MethodGet, "/ui/operator", "user-token", nil)
		if status != http.StatusForbidden {
			t.Fatalf("ordinary user GET status=%d body=%s", status, body)
		}
		status, _, body = request(t, http.MethodPost, "/ui/operator/audit-probe", "user-token", url.Values{"_csrf": {"forged"}})
		if status != http.StatusForbidden {
			t.Fatalf("ordinary user POST status=%d body=%s", status, body)
		}
	})

	t.Run("cross_organisation_state_isolated_by_trusted_identity", func(t *testing.T) {
		status, _, body := request(t, http.MethodGet, "/ui/operator", "other-token", nil)
		if status != http.StatusOK {
			t.Fatalf("other operator status=%d body=%s", status, body)
		}
		if !strings.Contains(body, "other/private") || !strings.Contains(body, "other-secret-skill") {
			t.Fatalf("other organisation state missing: %s", body)
		}
		for _, leaked := range []string{"demo/skills", "bad-skill"} {
			if strings.Contains(body, leaked) {
				t.Fatalf("cross-organisation leak %q: %s", leaked, body)
			}
		}
	})

	t.Run("state_change_requires_identity_bound_csrf_and_keeps_local_audit_on_export_failure", func(t *testing.T) {
		status, _, body := request(t, http.MethodGet, "/ui/operator", "operator-token", nil)
		if status != http.StatusOK {
			t.Fatalf("operator GET status=%d body=%s", status, body)
		}
		match := regexp.MustCompile(`name="_csrf" value="([a-f0-9]+)"`).FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatalf("CSRF token missing from authorised operator form: %s", body)
		}
		csrf := match[1]

		status, _, _ = request(t, http.MethodPost, "/ui/operator/audit-probe", "operator-token", url.Values{})
		if status != http.StatusForbidden {
			t.Fatalf("missing-CSRF POST status=%d", status)
		}
		status, _, _ = request(t, http.MethodPost, "/ui/operator/audit-probe", "other-token", url.Values{"_csrf": {csrf}})
		if status != http.StatusForbidden {
			t.Fatalf("cross-identity CSRF POST status=%d", status)
		}

		beforeAttempts, beforeFailures := exporter.Attempts(), exporter.Failures()
		status, headers, body := request(t, http.MethodPost, "/ui/operator/audit-probe", "operator-token", url.Values{"_csrf": {csrf}})
		if status != http.StatusSeeOther {
			t.Fatalf("authorised probe status=%d body=%s", status, body)
		}
		if headers.Get("Location") != "/ui/operator?probe=recorded" {
			t.Fatalf("probe redirect=%q", headers.Get("Location"))
		}
		if exporter.Attempts() != beforeAttempts+1 || exporter.Failures() != beforeFailures+1 {
			t.Fatalf("export attempts/failures=%d/%d before=%d/%d", exporter.Attempts(), exporter.Failures(), beforeAttempts, beforeFailures)
		}
		var actorType, actorID, requestID string
		if err := db.QueryRowContext(ctx, `SELECT actor_type, actor_id, request_id FROM audit_events WHERE organization_id='demo' AND event_type='operator_audit_probe' ORDER BY rowid DESC LIMIT 1`).Scan(&actorType, &actorID, &requestID); err != nil {
			t.Fatalf("local operator audit missing after exporter failure: %v", err)
		}
		if actorType != "human" || actorID != "operator" || requestID == "" {
			t.Fatalf("operator audit metadata=%q/%q request=%q", actorType, actorID, requestID)
		}
	})
}

func seedOperatorRepository(t *testing.T, ctx context.Context, catalog *catalogue.Store, organizationID, repositoryID, repositoryURL string) {
	t.Helper()
	if err := catalog.EnsureRepository(ctx, catalogue.Repository{ID: repositoryID, OrganizationID: organizationID, URL: repositoryURL, Ref: "main", TrustLevel: "approved", Owner: "platform"}); err != nil {
		t.Fatal(err)
	}
}

func seedOperatorQuarantine(t *testing.T, ctx context.Context, catalog *catalogue.Store, organizationID, repositoryID, name string) {
	t.Helper()
	skill := discovery.Skill{
		RelativePath: name,
		State:        discovery.Quarantined,
		Version:      "01.2.3",
		Frontmatter: skillspec.Frontmatter{
			Name:        name,
			Description: "quarantined operator fixture",
			Metadata:    map[string]string{"version": "01.2.3"},
		},
		Findings: []skillspec.Finding{{Code: skillspec.FindingInvalidVersion, Message: "invalid fixture version"}},
	}
	if err := catalog.RecordQuarantine(ctx, catalogue.Repository{ID: repositoryID, OrganizationID: organizationID}, skill, "commit-"+name, "tree-"+name); err != nil {
		t.Fatal(err)
	}
}
