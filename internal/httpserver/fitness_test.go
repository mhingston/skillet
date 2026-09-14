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

func TestImprovementFitnessToolsAreDefaultOffAndExplicitlyOptIn(t *testing.T) {
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

	t.Setenv(improvementFitnessEnv, "")
	off := listedToolNames(t, ctx, app)
	for _, name := range fitnessToolNames() {
		if off[name] {
			t.Fatalf("default-off installation exposed fitness tool %q", name)
		}
	}

	t.Setenv(improvementFitnessEnv, "true")
	on := listedToolNames(t, ctx, app)
	for _, name := range fitnessToolNames() {
		if !on[name] {
			t.Fatalf("opted-in installation did not expose fitness tool %q; tools=%v", name, on)
		}
	}
}

func fitnessToolNames() []string {
	return []string{
		"record_fitness_evidence",
		"get_fitness_evidence",
		"list_revision_fitness_evidence",
		"create_fitness_promotion_policy",
		"get_fitness_promotion_policy",
		"compare_fitness_evidence",
		"get_fitness_comparison",
	}
}
