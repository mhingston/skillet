package gitstore

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// CommandRunner is intentionally injectable so synchronization tests never need
// network access or a real repository.
type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

type Entry struct{ Mode, ObjectType, ObjectID, Path string }
type Mirror struct {
	Root string
	Git  CommandRunner
}

func NewMirror(root string) *Mirror { return &Mirror{Root: root, Git: execRunner{}} }

func (m *Mirror) Init(ctx context.Context, remoteURL string) error {
	if err := os.MkdirAll(filepath.Dir(m.Root), 0700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(m.Root, "HEAD")); os.IsNotExist(err) {
		if _, err := m.Git.Run(ctx, "git", "init", "--bare", m.Root); err != nil {
			return err
		}
		if _, err := m.Git.Run(ctx, "git", "--git-dir", m.Root, "remote", "add", "origin", remoteURL); err != nil {
			return err
		}
	}
	return nil
}

var validRef = regexp.MustCompile(`^(refs/(heads|tags)/[A-Za-z0-9._/-]+|[A-Za-z0-9._/-]+)$`)

func (m *Mirror) Fetch(ctx context.Context, ref string) (string, error) {
	if !validRef.MatchString(ref) || strings.Contains(ref, "..") || strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("unsupported git ref %q", ref)
	}
	if _, err := m.Git.Run(ctx, "git", "--git-dir", m.Root, "fetch", "--prune", "origin", ref); err != nil {
		return "", err
	}
	b, err := m.Git.Run(ctx, "git", "--git-dir", m.Root, "rev-parse", "FETCH_HEAD^{commit}")
	return strings.TrimSpace(string(b)), err
}

func (m *Mirror) ListTree(ctx context.Context, commit string) ([]Entry, error) {
	b, err := m.Git.Run(ctx, "git", "--git-dir", m.Root, "ls-tree", "-r", "-z", commit)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, record := range bytes.Split(b, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		parts := bytes.SplitN(record, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid ls-tree record")
		}
		fields := strings.Fields(string(parts[0]))
		if len(fields) != 3 {
			return nil, fmt.Errorf("invalid ls-tree header")
		}
		entries = append(entries, Entry{Mode: fields[0], ObjectType: fields[1], ObjectID: fields[2], Path: string(parts[1])})
	}
	return entries, nil
}

func (m *Mirror) ReadBlob(ctx context.Context, objectID string) ([]byte, error) {
	return m.Git.Run(ctx, "git", "--git-dir", m.Root, "cat-file", "blob", objectID)
}
func (m *Mirror) TreeID(ctx context.Context, commit, root string) (string, error) {
	if root == "" || strings.Contains(root, "..") || strings.HasPrefix(root, "/") {
		return "", fmt.Errorf("unsafe tree path %q", root)
	}
	b, err := m.Git.Run(ctx, "git", "--git-dir", m.Root, "rev-parse", commit+":"+root)
	return strings.TrimSpace(string(b)), err
}
