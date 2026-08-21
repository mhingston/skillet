package gitstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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

// Source is the immutable snapshot interface consumed by ingestion. Mirror
// implements it for remote Git repositories; LocalSource implements it for a
// plain local directory without modifying that directory.
type Source interface {
	Fetch(context.Context, string) (string, error)
	ListTree(context.Context, string) ([]Entry, error)
	ReadBlob(context.Context, string) ([]byte, error)
	TreeID(context.Context, string, string) (string, error)
}

type Mirror struct {
	Root string
	Git  CommandRunner
}

var _ Source = (*Mirror)(nil)

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

// LocalSource snapshots a directory into content-addressed entries. A source
// directory may be a Git working tree, but Git is not required: the snapshot
// identity is derived from paths, modes, types, and file contents.
type LocalSource struct {
	Root    string
	commit  string
	entries []Entry
	blobs   map[string][]byte
}

func NewLocalSource(root string) (*LocalSource, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve local source: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("local source: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("local source must be a directory: %q", absolute)
	}
	return &LocalSource{Root: absolute}, nil
}

var _ Source = (*LocalSource)(nil)

func (s *LocalSource) Fetch(ctx context.Context, _ string) (string, error) {
	if s == nil || s.Root == "" {
		return "", fmt.Errorf("local source is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	entries := make([]Entry, 0)
	blobs := make(map[string][]byte)
	err := filepath.WalkDir(s.Root, func(path string, dirEntry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if relative == ".git" || strings.HasPrefix(relative, ".git/") {
			if dirEntry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := dirEntry.Info()
		if err != nil {
			return err
		}
		mode := "100644"
		objectType := "blob"
		var content []byte
		switch {
		case dirEntry.Type()&os.ModeSymlink != 0:
			mode, objectType = "120000", "symlink"
			link, linkErr := os.Readlink(path)
			content, err = []byte(link), linkErr
		case dirEntry.IsDir():
			return nil
		case info.Mode().IsRegular():
			if info.Mode()&0111 != 0 {
				mode = "100755"
			}
			content, err = os.ReadFile(path)
		default:
			mode, objectType = "160000", "special"
		}
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		objectID := hex.EncodeToString(digest[:])
		if objectType == "blob" {
			blobs[objectID] = append([]byte(nil), content...)
		}
		entries = append(entries, Entry{Mode: mode, ObjectType: objectType, ObjectID: objectID, Path: relative})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("snapshot local source: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	var snapshot strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&snapshot, "%s\x00%s\x00%s\x00%s\x00", entry.Mode, entry.ObjectType, entry.ObjectID, entry.Path)
	}
	digest := sha256.Sum256([]byte(snapshot.String()))
	s.commit = "local-" + hex.EncodeToString(digest[:])
	s.entries = entries
	s.blobs = blobs
	return s.commit, nil
}

func (s *LocalSource) ListTree(ctx context.Context, commit string) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || commit == "" || commit != s.commit {
		return nil, fmt.Errorf("local snapshot %q is unavailable", commit)
	}
	return append([]Entry(nil), s.entries...), nil
}

func (s *LocalSource) ReadBlob(ctx context.Context, objectID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, ok := s.blobs[objectID]
	if !ok {
		return nil, fmt.Errorf("local blob %q is unavailable", objectID)
	}
	return append([]byte(nil), content...), nil
}

func (s *LocalSource) TreeID(ctx context.Context, commit, root string) (string, error) {
	entries, err := s.ListTree(ctx, commit)
	if err != nil {
		return "", err
	}
	if root == "" || strings.Contains(root, "..") || strings.HasPrefix(root, "/") {
		return "", fmt.Errorf("unsafe tree path %q", root)
	}
	var tree strings.Builder
	for _, entry := range entries {
		if entry.Path != root && !strings.HasPrefix(entry.Path, root+"/") {
			continue
		}
		fmt.Fprintf(&tree, "%s\x00%s\x00%s\x00%s\x00", entry.Mode, entry.ObjectType, entry.ObjectID, entry.Path)
	}
	if tree.Len() == 0 {
		return "", fmt.Errorf("local tree %q is unavailable", root)
	}
	digest := sha256.Sum256([]byte(tree.String()))
	return "local-tree-" + hex.EncodeToString(digest[:]), nil
}
