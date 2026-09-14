package e2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	authn "github.com/mhingston/skillet/internal/auth"
	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/composition"
	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
)

type compositionBrowserValidator struct{}

func (compositionBrowserValidator) Authenticate(string) (authn.Identity, error) {
	return authn.Identity{
		Subject: "composition-browser", OrganizationID: "demo",
		Permissions: map[string]struct{}{"capability.reader": {}},
	}, nil
}

func TestCompositionBrowserAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil { t.Fatal(err) }
	defer db.Close()
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)

	repositoryRoot := filepath.Join(root, "central")
	writeCompositionSkill(t, repositoryRoot, "review-base", "base review capability", "1.2.0", nil)
	writeCompositionSkill(t, repositoryRoot, "review-root", "root review capability", "2.0.0", map[string]string{
		composition.MetadataRequires: `[{"id":"demo/central/review-base","version":"^1.0.0"}]`,
		composition.MetadataRecommends: `[{"id":"demo/central/optional-context"}]`,
		composition.MetadataConflicts: `[{"id":"demo/central/optional-context"}]`,
	})
	writeCompositionSkill(t, repositoryRoot, "optional-context", "optional context capability", "1.0.0", nil)
	result := syncM1Repository(t, ctx, repositoryRoot, "central", catalog, packages)
	if result.Admitted != 3 || result.Quarantined != 0 { t.Fatalf("admission=%+v", result) }

	docs, err := catalog.RoutingDocuments(ctx, "demo")
	if err != nil { t.Fatal(err) }
	rootRevisionID := ""
	for _, doc := range docs {
		if doc.SkillID == "demo/central/review-root" {
			rootRevisionID = doc.ID
			break
		}
	}
	if rootRevisionID == "" { t.Fatal("review-root revision missing from routing documents") }
	index, err := search.New(nil)
	if err != nil { t.Fatal(err) }
	if err := index.Rebuild(docs); err != nil { t.Fatal(err) }
	capabilities, err := capability.New(index, []capability.SourcePolicy{{RepositoryID: "central", Scope: mustCapabilityScope(t, "demo", "", "")}})
	if err != nil { t.Fatal(err) }
	app := httpserver.NewComplete(nil, nil, index, "demo", candidate.Signer{Key: []byte("composition-browser-candidate-key")}, packages, packageurl.Signer{Key: []byte("composition-browser-package-key")}, catalog, "http://example.invalid")
	app.ConfigureCapabilities(capabilities)
	if err := app.ConfigureCollections([]composition.Collection{{
		ID: "review-kit", Name: "Review kit", Description: "Curated review capability set",
		Members: []composition.Reference{{ID: "demo/central/review-root", Version: "^2.0.0"}},
	}}); err != nil { t.Fatal(err) }
	defer app.ConfigureCollections(nil)
	defer app.ConfigureAuthorization(nil)

	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "static", OrganizationID: "demo", Validator: compositionBrowserValidator{}}))
	defer server.Close()
	get := func(path string) (int, string) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if reqErr != nil { t.Fatal(reqErr) }
		req.Header.Set("Authorization", "Bearer composition-browser-token")
		resp, requestErr := server.Client().Do(req)
		if requestErr != nil { t.Fatal(requestErr) }
		defer resp.Body.Close()
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil { t.Fatal(readErr) }
		return resp.StatusCode, string(body)
	}

	t.Run("capability_detail_shows_declared_composition", func(t *testing.T) {
		status, body := get("/ui/catalogue/" + url.PathEscape(rootRevisionID))
		if status != http.StatusOK { t.Fatalf("status=%d body=%s", status, body) }
		for _, want := range []string{
			composition.MetadataRequires, "demo/central/review-base",
			composition.MetadataRecommends, composition.MetadataConflicts, "demo/central/optional-context",
		} {
			if !strings.Contains(body, want) { t.Fatalf("capability detail missing %q in %s", want, body) }
		}
	})

	t.Run("valid_plan_preview", func(t *testing.T) {
		status, body := get("/ui/composition/preview?collection=review-kit")
		if status != http.StatusOK { t.Fatalf("status=%d body=%s", status, body) }
		for _, want := range []string{
			"Locked resolution plan", "demo/central/review-base", "demo/central/review-root",
			"Declared reasons and edges", "Recommended, not selected automatically", "Preview only",
			"No capability was executed or activated",
		} {
			if !strings.Contains(body, want) { t.Fatalf("missing %q in %s", want, body) }
		}
		if strings.Contains(body, "<script") { t.Fatalf("preview contains executable script markup: %s", body) }
	})

	t.Run("invalid_plan_fails_closed", func(t *testing.T) {
		status, body := get("/ui/composition/preview?collection=missing")
		if status != http.StatusBadRequest { t.Fatalf("status=%d body=%s", status, body) }
		if !strings.Contains(body, "Plan unavailable") || strings.Contains(body, "review-base") {
			t.Fatalf("unexpected invalid plan response: %s", body)
		}
	})

	t.Run("unauthorised_dependency_fails_without_identity_leak", func(t *testing.T) {
		policy, policyErr := authz.NewClaimsPolicy([]authz.Grant{{
			Permissions: []string{"capability.reader"},
			Actions: []authz.Action{authz.ActionCapabilityDescribe},
			Resources: []authz.ResourceRule{{IDs: []string{"demo/central/review-root"}}},
		}})
		if policyErr != nil { t.Fatal(policyErr) }
		app.ConfigureAuthorization(policy)
		status, body := get("/ui/composition/preview?collection=review-kit")
		if status != http.StatusBadRequest { t.Fatalf("status=%d body=%s", status, body) }
		if !strings.Contains(body, "Plan unavailable") { t.Fatalf("missing refusal: %s", body) }
		if strings.Contains(body, "demo/central/review-base") || strings.Contains(body, "review-base") {
			t.Fatalf("unauthorised dependency identity leaked: %s", body)
		}
	})
}

func writeCompositionSkill(t *testing.T, sourceRoot, name, description, version string, metadata map[string]string) {
	t.Helper()
	dir := filepath.Join(sourceRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n"
	values := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		values[key] = value
	}
	if version != "" {
		values["version"] = version
	}
	if len(values) > 0 {
		body += "metadata:\n"
		for _, key := range []string{"version", composition.MetadataRequires, composition.MetadataRecommends, composition.MetadataConflicts} {
			if value, ok := values[key]; ok {
				body += "  " + key + ": " + strconv.Quote(value) + "\n"
			}
		}
	}
	body += "---\n# " + name + "\n\nDeterministic composition fixture.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
