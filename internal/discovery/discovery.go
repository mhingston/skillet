// Package discovery discovers independently addressable skills from a
// repository tree. It does not clone repositories, execute files, or write
// packages.
package discovery

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/mhingston/skillet/internal/skillspec"
)

type EntryKind uint8

const (
	RegularFile EntryKind = iota + 1
	Directory
	Symlink
	Device
)

type TreeEntry struct {
	Path string
	Kind EntryKind
}

type MetadataRule struct {
	Key    string
	Equals string
}

type Options struct {
	Include          []string
	Exclude          []string
	SearchExclusions []MetadataRule
}

type State string

const (
	Admitted    State = "admitted"
	Quarantined State = "quarantined"
)

type Skill struct {
	RelativePath string
	Entrypoint   string
	Frontmatter  skillspec.Frontmatter
	Findings     []skillspec.Finding
	Searchable   bool
	State        State
	HasScripts   bool
}

// Discover validates tree safety before reading any SKILL.md, then returns a
// deterministic result ordered by entrypoint. Read errors are fatal and never
// produce a partial result. Invalid frontmatter is represented as quarantined
// data so callers can persist admission findings without indexing it.
func Discover(entries []TreeEntry, readFile func(string) ([]byte, error), options Options) ([]Skill, error) {
	if readFile == nil {
		return nil, fmt.Errorf("readFile is required")
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		clean, err := safePath(entry.Path)
		if err != nil {
			return nil, err
		}
		if clean != entry.Path {
			return nil, fmt.Errorf("repository entry %q is not canonical", entry.Path)
		}
		if _, exists := seen[clean]; exists {
			return nil, fmt.Errorf("duplicate repository entry %q", clean)
		}
		seen[clean] = struct{}{}
		if entry.Kind == Symlink || entry.Kind == Device {
			return nil, fmt.Errorf("repository entry %q has unsupported kind", clean)
		}
		if entry.Kind != RegularFile && entry.Kind != Directory {
			return nil, fmt.Errorf("repository entry %q has unknown kind", clean)
		}
	}

	var candidates []TreeEntry
	for _, entry := range entries {
		if entry.Kind == RegularFile && path.Base(entry.Path) == "SKILL.md" && matchesAny(entry.Path, options.Include, true) && !matchesAny(entry.Path, options.Exclude, false) {
			candidates = append(candidates, entry)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	result := make([]Skill, 0, len(candidates))
	for _, candidate := range candidates {
		content, err := readFile(candidate.Path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", candidate.Path, err)
		}
		doc, err := skillspec.Parse(content)
		if err != nil {
			result = append(result, Skill{
				RelativePath: path.Dir(candidate.Path),
				Entrypoint:   candidate.Path,
				Findings:     []skillspec.Finding{{Code: skillspec.FindingMalformedFrontmatter, Message: err.Error()}},
				Searchable:   false,
				State:        Quarantined,
			})
			continue
		}
		findings := skillspec.Validate(doc.Frontmatter)
		root := path.Dir(candidate.Path)
		if doc.Frontmatter.Name != path.Base(root) {
			findings = append(findings, skillspec.Finding{Code: skillspec.FindingNameDirectoryMismatch, Message: fmt.Sprintf("name %q must match skill directory %q", doc.Frontmatter.Name, path.Base(root))})
		}
		searchable := len(findings) == 0 && !excludedByMetadata(doc.Frontmatter.Metadata, options.SearchExclusions)
		state := Admitted
		if len(findings) != 0 {
			state = Quarantined
		}
		hasScripts := false
		for _, entry := range entries {
			if entry.Kind == RegularFile && (entry.Path == root+"/scripts" || strings.HasPrefix(entry.Path, root+"/scripts/")) {
				hasScripts = true
				break
			}
		}
		result = append(result, Skill{RelativePath: root, Entrypoint: candidate.Path, Frontmatter: doc.Frontmatter, Findings: findings, Searchable: searchable, State: state, HasScripts: hasScripts})
	}
	return result, nil
}

func safePath(value string) (string, error) {
	if value == "" || strings.Contains(value, `\`) || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("unsafe repository path %q", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe repository path %q", value)
	}
	return clean, nil
}

func excludedByMetadata(metadata map[string]string, rules []MetadataRule) bool {
	for _, rule := range rules {
		if metadata[rule.Key] == rule.Equals {
			return true
		}
	}
	return false
}

// matchesAny supports slash-separated repository globs, including the common
// ** form used in configuration, without interpreting paths as filesystem
// paths. An empty pattern list means allow-all for include and deny-none for
// exclude, controlled by the allowEmpty argument.
func matchesAny(value string, patterns []string, allowEmpty bool) bool {
	if len(patterns) == 0 {
		return allowEmpty
	}
	for _, pattern := range patterns {
		if glob(path.Clean(pattern), value) {
			return true
		}
	}
	return false
}

func glob(pattern, value string) bool {
	pp, vv := strings.Split(pattern, "/"), strings.Split(value, "/")
	var match func(int, int) bool
	match = func(i, j int) bool {
		if i == len(pp) {
			return j == len(vv)
		}
		if pp[i] == "**" {
			return match(i+1, j) || (j < len(vv) && match(i, j+1))
		}
		if j == len(vv) {
			return false
		}
		ok, err := path.Match(pp[i], vv[j])
		return err == nil && ok && match(i+1, j+1)
	}
	return match(0, 0)
}
