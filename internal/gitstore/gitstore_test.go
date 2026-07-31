package gitstore

import (
	"context"
	"strings"
	"testing"
)

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
