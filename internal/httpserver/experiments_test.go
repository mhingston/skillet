package httpserver

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestImprovementExperimentToolsAreDefaultOffAndExplicitlyOptIn(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)
	app := NewComplete(nil, nil, nil, "demo", candidate.Signer{Key: []byte("candidate-test-key")}, packages, packageurl.Signer{Key: []byte("package-test-key")}, catalog, "http://example.invalid")

	t.Setenv(improvementExperimentsEnv, "")
	off := listedToolNames(t, ctx, app)
	for _, name := range experimentToolNames() {
		if off[name] {
			t.Fatalf("default-off installation exposed experiment tool %q", name)
		}
	}

	t.Setenv(improvementExperimentsEnv, "true")
	on := listedToolNames(t, ctx, app)
	for _, name := range experimentToolNames() {
		if !on[name] {
			t.Fatalf("opted-in installation did not expose experiment tool %q; tools=%v", name, on)
		}
	}
}

func listedToolNames(t *testing.T, ctx context.Context, app *Server) map[string]bool {
	t.Helper()
	ts := httptest.NewServer(app.Handler("/mcp", 1<<20, AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer ts.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "experiment-tool-surface-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		seen[tool.Name] = true
	}
	return seen
}

func experimentToolNames() []string {
	return []string{
		"create_improvement_experiment",
		"get_improvement_experiment",
		"list_improvement_experiments",
		"submit_improvement_experiment_result",
		"cancel_improvement_experiment",
	}
}
