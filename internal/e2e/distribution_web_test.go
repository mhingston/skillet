package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	authn "github.com/mhingston/skillet/internal/auth"
	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/composition"
	"github.com/mhingston/skillet/internal/distribution"
	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
)

type distributionBrowserValidator struct{}

func (distributionBrowserValidator) Authenticate(string) (authn.Identity, error) {
	return authn.Identity{
		Subject:          "distribution-browser",
		OrganizationID:   "demo",
		Permissions:      map[string]struct{}{"capability.reader": {}},
	}, nil
}

func TestDistributionBrowserAcceptance(t *testing.T) {
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
	writeCompositionSkill(t, centralRoot, "review-base", "base review capability", "1.2.0", nil)
	writeCompositionSkill(t, centralRoot, "review-root", "root review capability", "2.0.0", map[string]string{
		composition.MetadataRequires: `[{"id":"demo/central/review-base","version":"^1.0.0"}]`,
	})
	writeCompositionSkill(t, centralRoot, "old-review", "legacy review capability", "0.9.0", map[string]string{
		governance.StateKey:  string(capability.StatusDeprecated),
		governance.ReasonKey: "legacy fixture",
	})
	writeCompositionSkill(t, centralRoot, "withdrawn-review", "withdrawn fixture must not leak", "1.0.0", map[string]string{
		governance.StateKey:  string(capability.StatusYanked),
		governance.ReasonKey: "withdrawn fixture",
	})
	if result := syncM1Repository(t, ctx, centralRoot, "central", catalog, packages); result.Admitted != 4 || result.Quarantined != 0 {
		t.Fatalf("central admission=%+v", result)
	}

	privateRoot := filepath.Join(root, "private")
	writeCompositionSkill(t, privateRoot, "private-review", "restricted fixture must not leak", "1.0.0", nil)
	if result := syncM1Repository(t, ctx, privateRoot, "private", catalog, packages); result.Admitted != 1 || result.Quarantined != 0 {
		t.Fatalf("private admission=%+v", result)
	}
	if _, err := db.ExecContext(ctx, `UPDATE repositories SET url=? WHERE id=?`, "https://github.com/example/central-skills.git", "demo/central"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE repositories SET url=? WHERE id=?`, "https://github.com/example/private-skills.git", "demo/private"); err != nil {
		t.Fatal(err)
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
	privateScope, err := capability.NewScope("demo", "team", "private")
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := capability.New(index, []capability.SourcePolicy{
		{RepositoryID: "central", Scope: mustCapabilityScope(t, "demo", "", "")},
		{RepositoryID: "private", Scope: privateScope},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := httpserver.NewComplete(nil, nil, index, "demo", candidate.Signer{Key: []byte("distribution-candidate-key")}, packages, packageurl.Signer{Key: []byte("distribution-package-key")}, catalog, "http://example.invalid")
	app.ConfigureCapabilities(capabilities)
	if err := app.ConfigureCollections([]composition.Collection{{
		ID:          "review-kit",
		Name:        `Review kit <script>alert("collection")</script>`,
		Description: "root plus declared dependency",
		Members:     []composition.Reference{{ID: "demo/central/review-root", Version: "^2.0.0"}},
	}}); err != nil {
		t.Fatal(err)
	}
	defer app.ConfigureCollections(nil)
	defer app.ConfigureAuthorization(nil)

	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "static", OrganizationID: "demo", Validator: distributionBrowserValidator{}}))
	defer server.Close()
	get := func(path string) (int, http.Header, []byte) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		req.Header.Set("Authorization", "Bearer distribution-token")
		resp, requestErr := server.Client().Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer resp.Body.Close()
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return resp.StatusCode, resp.Header.Clone(), body
	}

	t.Run("authorized_manifest_is_stable_pinned_and_scope_filtered", func(t *testing.T) {
		status, headers, first := get("/v1/distribution/claude-code/marketplace.json")
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, first)
		}
		status, secondHeaders, second := get("/v1/distribution/claude-code/marketplace.json")
		if status != http.StatusOK || !bytes.Equal(first, second) {
			t.Fatalf("same authorized snapshot not byte stable: status=%d", status)
		}
		if headers.Get("ETag") == "" || headers.Get("ETag") != secondHeaders.Get("ETag") || headers.Get("X-Skillet-Distribution-Profile") != distribution.ClaudeCodeProfile {
			t.Fatalf("missing/stable distribution provenance headers: %v", headers)
		}
		var manifest struct {
			Name    string `json:"name"`
			Plugins []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Strict      bool   `json:"strict"`
				Skills      []string `json:"skills"`
				Source      struct {
					Source string `json:"source"`
					URL    string `json:"url"`
					Path   string `json:"path"`
					SHA    string `json:"sha"`
				} `json:"source"`
			} `json:"plugins"`
		}
		if err := json.Unmarshal(first, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Name == "" || len(manifest.Plugins) != 3 {
			t.Fatalf("expected active+deprecated central skills only, got %+v", manifest)
		}
		paths := map[string]bool{}
		deprecatedVisible := false
		for _, plugin := range manifest.Plugins {
			paths[plugin.Source.Path] = true
			if plugin.Source.Source != "git-subdir" || plugin.Source.URL != "https://github.com/example/central-skills.git" || len(plugin.Source.SHA) != 40 || plugin.Strict || len(plugin.Skills) != 1 || plugin.Skills[0] != "./" {
				t.Fatalf("host plugin is not a pinned raw skill subdirectory: %+v", plugin)
			}
			if strings.HasPrefix(plugin.Description, "DEPRECATED: ") {
				deprecatedVisible = true
			}
		}
		for _, want := range []string{"review-base", "review-root", "old-review"} {
			if !paths[want] {
				t.Fatalf("missing expected skill path %q: %+v", want, paths)
			}
		}
		if !deprecatedVisible {
			t.Fatal("deprecated capability was not explicit")
		}
		for _, forbidden := range []string{"withdrawn-review", "withdrawn fixture", "private-review", "restricted fixture"} {
			if strings.Contains(string(first), forbidden) {
				t.Fatalf("unauthorized/yanked metadata leaked: %q", forbidden)
			}
		}
	})

	t.Run("browser_view_has_provenance_commands_escaping_and_no_secret", func(t *testing.T) {
		status, _, body := get("/ui/distribution")
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, body)
		}
		text := string(body)
		for _, want := range []string{"Claude Code marketplace export", "Download marketplace.json", "claude plugin marketplace add ./skillet-marketplace", "Manifest SHA-256", "review-root", "2.0.0", "review-kit", "&lt;script&gt;"} {
			if !strings.Contains(text, want) {
				t.Fatalf("distribution UI missing %q in %s", want, text)
			}
		}
		for _, forbidden := range []string{"distribution-token", "withdrawn-review", "private-review", `<script>alert("collection")</script>`} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("distribution UI leaked or rendered unsafe value %q", forbidden)
			}
		}
	})

	t.Run("collection_exports_exact_dependency_closure", func(t *testing.T) {
		status, _, body := get("/v1/distribution/claude-code/marketplace.json?collection=review-kit")
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, body)
		}
		text := string(body)
		for _, want := range []string{"review-base", "review-root"} {
			if !strings.Contains(text, want) {
				t.Fatalf("collection missing %q: %s", want, text)
			}
		}
		for _, forbidden := range []string{"old-review", "withdrawn-review", "private-review"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("collection leaked unrelated capability %q: %s", forbidden, text)
			}
		}
	})

	t.Run("repository_scope_includes_central_and_matching_local_only", func(t *testing.T) {
		status, _, body := get("/v1/distribution/claude-code/marketplace.json?namespace=team&repository=private")
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, body)
		}
		text := string(body)
		for _, want := range []string{"private-review", "review-base", "review-root", "old-review"} {
			if !strings.Contains(text, want) {
				t.Fatalf("scope projection missing %q: %s", want, text)
			}
		}
		for _, forbidden := range []string{"withdrawn-review", "withdrawn fixture"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("scope projection leaked %q: %s", forbidden, text)
			}
		}
	})

	t.Run("m2_denial_filters_entries_and_collection_fails_without_dependency_leak", func(t *testing.T) {
		policy, policyErr := authz.NewClaimsPolicy([]authz.Grant{{
			Permissions: []string{"capability.reader"},
			Actions:     []authz.Action{authz.ActionCapabilitySearch, authz.ActionCapabilityDescribe},
			Resources:   []authz.ResourceRule{{IDs: []string{"demo/central/review-root"}}},
		}})
		if policyErr != nil {
			t.Fatal(policyErr)
		}
		app.ConfigureAuthorization(policy)
		defer app.ConfigureAuthorization(nil)

		status, _, body := get("/v1/distribution/claude-code/marketplace.json")
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if !strings.Contains(string(body), "review-root") {
			t.Fatalf("authorized item missing: %s", body)
		}
		for _, forbidden := range []string{"review-base", "old-review", "private-review", "withdrawn-review"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("denied identity leaked: %q in %s", forbidden, body)
			}
		}

		status, _, body = get("/v1/distribution/claude-code/marketplace.json?collection=review-kit")
		if status != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if strings.Contains(string(body), "review-base") {
			t.Fatalf("denied dependency leaked in refusal: %s", body)
		}
	})
}
