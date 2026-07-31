package ingest

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/gitstore"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/store"
)

type runner struct{ outputs map[string][]byte }

func (r runner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	return r.outputs[name+" "+strings.Join(args, " ")], nil
}

func TestSyncOnceAdmitsValidatedSkillOnlyAfterCASPromotion(t *testing.T) {
	content := []byte("---\nname: plan\ndescription: Make plans\n---\n# Plan\n")
	root := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fake := runner{outputs: map[string][]byte{
		"git --git-dir /mirror fetch --prune origin refs/heads/main": nil,
		"git --git-dir /mirror rev-parse FETCH_HEAD^{commit}":        []byte("commit1\n"),
		"git --git-dir /mirror ls-tree -r -z commit1":                []byte("100644 blob object1\tplan/SKILL.md\x00"),
		"git --git-dir /mirror rev-parse commit1:plan":               []byte("tree1\n"),
		"git --git-dir /mirror cat-file blob object1":                content,
	}}
	packages := packagestore.New(filepath.Join(root, "packages"))
	result, err := SyncOnce(context.Background(), &gitstore.Mirror{Root: "/mirror", Git: fake}, catalogue.Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.com/skills.git", Ref: "refs/heads/main", TrustLevel: "approved", Owner: "team"}, packages, catalogue.New(db, packages), "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if result.Admitted != 1 || result.Quarantined != 0 {
		t.Fatalf("result = %+v", result)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM skill_revisions WHERE state='active'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("active revisions = %d", count)
	}
}

func TestGitModePreservesExecutableBit(t *testing.T) {
	if got := gitMode("100755"); got != 0755 {
		t.Fatalf("executable mode = %o, want 0755", got)
	}
	if got := gitMode("100644"); got != 0644 {
		t.Fatalf("regular mode = %o, want 0644", got)
	}
	if got := gitMode("bad"); got != 0644 {
		t.Fatalf("invalid mode = %o, want 0644", got)
	}
}
