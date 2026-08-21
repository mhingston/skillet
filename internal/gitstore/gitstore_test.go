package gitstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalSourceSnapshotsPlainDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "plan"), 0755); err != nil {
		t.Fatal(err)
	}
	content := []byte("---\nname: plan\ndescription: Make plans\n---\n# Plan\n")
	if err := os.WriteFile(filepath.Join(root, "plan", "SKILL.md"), content, 0644); err != nil {
		t.Fatal(err)
	}
	source, err := NewLocalSource(root)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := source.Fetch(context.Background(), "working-tree")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := source.ListTree(context.Background(), commit)
	if err != nil || len(entries) != 1 || entries[0].Path != "plan/SKILL.md" {
		t.Fatalf("entries = %+v, err = %v", entries, err)
	}
	blob, err := source.ReadBlob(context.Background(), entries[0].ObjectID)
	if err != nil || string(blob) != string(content) {
		t.Fatalf("blob = %q, err = %v", blob, err)
	}
	tree, err := source.TreeID(context.Background(), commit, "plan")
	if err != nil || !strings.HasPrefix(tree, "local-tree-") {
		t.Fatalf("tree = %q, err = %v", tree, err)
	}
	if _, err := source.Fetch(context.Background(), "working-tree"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ListTree(context.Background(), "old-snapshot"); err == nil {
		t.Fatal("stale local snapshot unexpectedly available")
	}
}

type fakeRunner struct {
	calls   []string
	outputs map[string][]byte
	errors  map[string]error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	return f.outputs[key], f.errors[key]
}

func TestListTreeParsesNulDelimitedGitOutput(t *testing.T) {
	f := &fakeRunner{outputs: map[string][]byte{"git --git-dir /mirror ls-tree -r -z abc": []byte("100644 blob one\talpha/SKILL.md\x00100644 blob two\tbeta/data.txt\x00")}}
	m := &Mirror{Root: "/mirror", Git: f}
	entries, err := m.ListTree(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Path != "alpha/SKILL.md" || entries[1].ObjectID != "two" {
		t.Fatalf("entries: %+v", entries)
	}
}

func TestFetchUsesExactConfiguredRefAndReturnsCommit(t *testing.T) {
	f := &fakeRunner{outputs: map[string][]byte{"git --git-dir /mirror fetch --prune origin refs/heads/main": nil, "git --git-dir /mirror rev-parse FETCH_HEAD^{commit}": []byte("abc123\n")}}
	m := &Mirror{Root: "/mirror", Git: f}
	got, err := m.Fetch(context.Background(), "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if got != "abc123" {
		t.Fatalf("commit %q", got)
	}
	if !strings.Contains(f.calls[0], "refs/heads/main") {
		t.Fatal(f.calls)
	}
}
