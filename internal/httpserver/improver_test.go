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

func TestImproverMetaEvalToolsAreDefaultOffAndExplicitlyOptIn(t *testing.T) {
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

	t.Setenv(improverMetaEvalEnv, "")
	off := listedToolNames(t, ctx, app)
	for _, name := range improverToolNames() {
		if off[name] {
			t.Fatalf("default-off installation exposed improver tool %q", name)
		}
	}

	t.Setenv(improverMetaEvalEnv, "true")
	on := listedToolNames(t, ctx, app)
	for _, name := range improverToolNames() {
		if !on[name] {
			t.Fatalf("opted-in installation did not expose improver tool %q; tools=%v", name, on)
		}
	}
}

func improverToolNames() []string {
	return []string{
		"record_improver_provenance",
		"get_improver_provenance",
		"create_improver_meta_eval",
		"get_improver_meta_eval",
		"evaluate_improver_strategies",
		"get_improver_meta_evaluation",
	}
}
