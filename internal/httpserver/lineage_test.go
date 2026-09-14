package httpserver

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/store"
)

func TestImprovementLineageToolsAreDefaultOffAndExplicitlyOptIn(t *testing.T) {
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

	t.Setenv(improvementLineageEnv, "")
	off := listedToolNames(t, ctx, app)
	for _, name := range lineageToolNames() {
		if off[name] {
			t.Fatalf("default-off installation exposed lineage tool %q", name)
		}
	}

	t.Setenv(improvementLineageEnv, "true")
	on := listedToolNames(t, ctx, app)
	for _, name := range lineageToolNames() {
		if !on[name] {
			t.Fatalf("opted-in installation did not expose lineage tool %q; tools=%v", name, on)
		}
	}
}

func lineageToolNames() []string {
	return []string{"record_revision_lineage", "record_lineage_decision", "get_revision_lineage"}
}
