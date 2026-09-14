package e2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	writeGovernanceSkill(t, repositoryRoot, "review-base", "base review capability", map[string]string{"version": "1.2.0"})
	writeGovernanceSkill(t, repositoryRoot, "review-root", "root review capability", map[string]string{
		"version": "2.0.0",
		composition.MetadataRequires: `[{"id":"demo/central/review-base","version":"^1.0.0"}]`,
		composition.MetadataRecommends: `[{"id":"demo/central/optional-context"}]`,
	})
	writeGovernanceSkill(t, repositoryRoot, "optional-context", "optional context capability", map[string]string{"version": "1.0.0"})
	result := syncM1Repository(t, ctx, repositoryRoot, "central", catalog, packages)
	if result.Admitted != 3 || result.Quarantined != 0 { t.Fatalf("admission=%+v", result) }

	docs, err := catalog.RoutingDocuments(ctx, "demo")
	if err != nil { t.Fatal(err) }
	index, err := search.New(nil)
	if err != nil { t.Fatal(err) }
	if err := index.Rebuild(docs); err != nil { t.Fatal(err) }
	capabilities, err := capability.New(index, []capability.SourcePolicy{{RepositoryID: "central", Scope: mustCapabilityScope(t, "demo", "", "")}})
	if err != nil { t.Fatal(err) }
	app := httpserver.NewComplete(nil, nil, index, "demo", candidate.Signer{Key: []byte("composition-browser-candidate-key")}, packages, packageurl.Signer{Key: []byte("composition-browser-package-key")}, catalog, "http://example.invalid")
	app.ConfigureCapabilities(capabilities)
	if err := app.ConfigureCollections([]composition.Collection{{
		ID: "review-kit", Name: "Review kit", Description: "Curated review workflow inputs",
		Members: []composition.Reference{{ID: "demo/central/review-root", Version: "^2.0.0"}},
	}}); err != nil { t.Fatal(err) }
	defer app.ConfigureCollections(nil)

	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "static", StaticToken: "composition-browser-token", OrganizationID: "demo"}))
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
}
