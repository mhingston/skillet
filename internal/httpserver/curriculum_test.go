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

func TestCurriculumToolsAreDefaultOffAndExplicitlyOptIn(t *testing.T) {
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

	t.Setenv(improvementCurriculumEnv, "")
	off := listedToolNames(t, ctx, app)
	for _, name := range curriculumToolNames() {
		if off[name] {
			t.Fatalf("default-off installation exposed curriculum tool %q", name)
		}
	}

	t.Setenv(improvementCurriculumEnv, "true")
	on := listedToolNames(t, ctx, app)
	for _, name := range curriculumToolNames() {
		if !on[name] {
			t.Fatalf("opted-in installation did not expose curriculum tool %q; tools=%v", name, on)
		}
	}
}

func curriculumToolNames() []string {
	return []string{
		"record_capability_gap",
		"get_capability_gap",
		"create_curriculum_proposal",
		"get_curriculum_proposal",
		"review_curriculum_proposal",
		"get_curriculum_review",
		"create_curriculum_eval_suite_version",
		"get_curriculum_eval_suite_version",
		"prepare_curriculum_handoff",
	}
}
