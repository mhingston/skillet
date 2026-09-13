package knowledge

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// OKFBundle describes one already-materialized Open Knowledge Format bundle.
// Harvesting source systems into OKF is deliberately outside Skillet core.
type OKFBundle struct {
	ID       string `json:"id"`
	Root     string `json:"root"`
	Locator  string `json:"locator"`
	Revision string `json:"revision"`
}

type SourceReference struct {
	ID           string      `json:"id,omitempty" yaml:"id,omitempty"`
	Resource     string      `json:"resource,omitempty" yaml:"resource,omitempty"`
	Title        string      `json:"title,omitempty" yaml:"title,omitempty"`
	Author       string      `json:"author,omitempty" yaml:"author,omitempty"`
	UsageCount   *int64      `json:"usage_count,omitempty" yaml:"usage_count,omitempty"`
	LastModified string      `json:"last_modified,omitempty" yaml:"last_modified,omitempty"`
	UsageWindow  *TimeWindow `json:"usage_window,omitempty" yaml:"usage_window,omitempty"`
}

type TimeWindow struct {
	From string `json:"from,omitempty" yaml:"from,omitempty"`
	To   string `json:"to,omitempty" yaml:"to,omitempty"`
}

type Generation struct {
	By string `json:"by,omitempty" yaml:"by,omitempty"`
	At string `json:"at,omitempty" yaml:"at,omitempty"`
}

type Verification struct {
	By string `json:"by,omitempty" yaml:"by,omitempty"`
	At string `json:"at,omitempty" yaml:"at,omitempty"`
}

type Verifications []Verification

func (v *Verifications) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var values []Verification
		if err := node.Decode(&values); err != nil {
			return err
		}
		*v = values
		return nil
	case yaml.MappingNode:
		var value Verification
		if err := node.Decode(&value); err != nil {
			return err
		}
		*v = []Verification{value}
		return nil
	default:
		return errors.New("verified must be a mapping or sequence")
	}
}

// Metadata is the OKF subset Skillet preserves and returns to callers. Extra
// keeps producer-defined keys as data so unknown metadata is not silently lost.
type Metadata struct {
	Type        string            `json:"type" yaml:"type"`
	Title       string            `json:"title,omitempty" yaml:"title,omitempty"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Resource    string            `json:"resource,omitempty" yaml:"resource,omitempty"`
	Tags        []string          `json:"tags,omitempty" yaml:"tags,omitempty"`
	Sources     []SourceReference `json:"sources,omitempty" yaml:"sources,omitempty"`
	UsageWindow *TimeWindow       `json:"usage_window,omitempty" yaml:"usage_window,omitempty"`
	Generated   *Generation       `json:"generated,omitempty" yaml:"generated,omitempty"`
	Verified    Verifications     `json:"verified,omitempty" yaml:"verified,omitempty"`
	Status      string            `json:"status,omitempty" yaml:"status,omitempty"`
	StaleAfter  string            `json:"stale_after,omitempty" yaml:"stale_after,omitempty"`
	Timestamp   string            `json:"timestamp,omitempty" yaml:"timestamp,omitempty"`
	Extra       map[string]any    `json:"extra,omitempty" yaml:"-"`
}

type Link struct {
	Text             string `json:"text,omitempty"`
	TargetPath       string `json:"target_path"`
	TargetDocumentID string `json:"target_document_id,omitempty"`
	Resolved         bool   `json:"resolved"`
}

type Backlink struct {
	DocumentID string `json:"document_id"`
	Path       string `json:"path"`
	Title      string `json:"title,omitempty"`
	LinkText   string `json:"link_text,omitempty"`
}

// OKFDocument pairs the existing knowledge document model with OKF-only
// provenance and explicit-link data.
type OKFDocument struct {
	Document Document `json:"document"`
	Metadata Metadata `json:"metadata"`
	Links    []Link   `json:"links,omitempty"`
}

type OKFSnapshot struct {
	Documents []Document             `json:"documents"`
	Chunks    []Chunk                `json:"chunks"`
	Details   map[string]OKFDocument `json:"-"`
}

type parsedOKFDocument struct {
	document Document
	metadata Metadata
	body     string
}

var markdownLinkPattern = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)(?:\s+["'][^"']*["'])?\)`)

// DiscoverOKF validates and maps local OKF bundles into the existing knowledge
// Document and Chunk types. It is deterministic and performs no network access.
func DiscoverOKF(bundles []OKFBundle) (OKFSnapshot, error) {
	ordered := append([]OKFBundle(nil), bundles...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	seenBundles := map[string]struct{}{}
	seenDocs := map[string]string{}
	snapshot := OKFSnapshot{Details: map[string]OKFDocument{}}

	for _, bundle := range ordered {
		bundle.ID = strings.TrimSpace(bundle.ID)
		if bundle.ID == "" {
			return OKFSnapshot{}, errors.New("OKF bundle id is required")
		}
		if _, ok := seenBundles[bundle.ID]; ok {
			return OKFSnapshot{}, fmt.Errorf("duplicate OKF bundle id %q", bundle.ID)
		}
		seenBundles[bundle.ID] = struct{}{}
		root, err := filepath.Abs(bundle.Root)
		if err != nil || strings.TrimSpace(bundle.Root) == "" {
			return OKFSnapshot{}, fmt.Errorf("resolve OKF bundle %q root", bundle.ID)
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return OKFSnapshot{}, fmt.Errorf("read OKF bundle %q: root must be a directory", bundle.ID)
		}
		locator := strings.TrimSpace(bundle.Locator)
		if locator == "" {
			locator = "file://" + filepath.ToSlash(root)
		}

		var files []string
		err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("unsafe OKF bundle path %q: symbolic links are not allowed", current)
			}
			if entry.IsDir() {
				if current != root && entry.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.EqualFold(filepath.Ext(entry.Name()), ".md") && !isOKFReserved(entry.Name()) {
				files = append(files, current)
			}
			return nil
		})
		if err != nil {
			return OKFSnapshot{}, fmt.Errorf("discover OKF bundle %q: %w", bundle.ID, err)
		}
		sort.Strings(files)

		parsed := make([]parsedOKFDocument, 0, len(files))
		byPath := map[string]string{}
		caseFolded := map[string]string{}
		for _, file := range files {
			relative, err := filepath.Rel(root, file)
			if err != nil {
				return OKFSnapshot{}, err
			}
			relative = filepath.ToSlash(filepath.Clean(relative))
			if unsafeRelativePath(relative) {
				return OKFSnapshot{}, fmt.Errorf("unsafe OKF bundle path %q", relative)
			}
			conceptID := strings.TrimSuffix(relative, path.Ext(relative))
			folded := strings.ToLower(conceptID)
			if previous, ok := caseFolded[folded]; ok && previous != conceptID {
				return OKFSnapshot{}, fmt.Errorf("duplicate OKF concept identity collision between %q and %q", previous, conceptID)
			}
			caseFolded[folded] = conceptID

			contents, err := os.ReadFile(file)
			if err != nil {
				return OKFSnapshot{}, err
			}
			if len(contents) > maxMarkdownBytes {
				return OKFSnapshot{}, fmt.Errorf("%w: %s/%s exceeds %d bytes", ErrMalformedMarkdown, bundle.ID, relative, maxMarkdownBytes)
			}
			if !utf8.Valid(contents) || bytes.IndexByte(contents, 0) >= 0 {
				return OKFSnapshot{}, fmt.Errorf("%w: %s/%s is not valid UTF-8 text", ErrMalformedMarkdown, bundle.ID, relative)
			}
			metadata, body, err := parseOKFConcept(contents)
			if err != nil {
				return OKFSnapshot{}, fmt.Errorf("parse OKF concept %s/%s: %w", bundle.ID, relative, err)
			}
			documentID := stableID("document", bundle.ID, relative)
			identity := bundle.ID + "/" + relative
			if previous, ok := seenDocs[documentID]; ok && previous != identity {
				return OKFSnapshot{}, fmt.Errorf("knowledge document id collision between %q and %q", previous, identity)
			}
			seenDocs[documentID] = identity
			document := Document{ID: documentID, SourceID: bundle.ID, SourceLocator: locator, SourceRevision: bundle.Revision, Path: relative, ContentDigest: digest(contents)}
			parsed = append(parsed, parsedOKFDocument{document: document, metadata: metadata, body: body})
			byPath[relative] = documentID
		}

		for _, item := range parsed {
			links := resolveOKFLinks(item.document.Path, item.body, byPath)
			snapshot.Documents = append(snapshot.Documents, item.document)
			snapshot.Details[item.document.ID] = OKFDocument{Document: item.document, Metadata: item.metadata, Links: links}
			sections, err := parseATXSections(item.body)
			if err != nil {
				return OKFSnapshot{}, err
			}
			for _, section := range sections {
				content := strings.TrimSpace(section.content)
				if content == "" && len(section.path) == 0 {
					continue
				}
				contentDigest := digest([]byte(content))
				headingPath := append([]string(nil), section.path...)
				heading := ""
				if len(headingPath) > 0 {
					heading = headingPath[len(headingPath)-1]
				}
				snapshot.Chunks = append(snapshot.Chunks, Chunk{ID: stableID("chunk", item.document.ID, strings.Join(headingPath, "\x1f"), contentDigest), DocumentID: item.document.ID, SourceID: bundle.ID, SourceLocator: locator, SourceRevision: bundle.Revision, Path: item.document.Path, DocumentDigest: item.document.ContentDigest, Heading: heading, HeadingPath: headingPath, Content: content, ContentDigest: contentDigest})
			}
		}
	}

	sort.Slice(snapshot.Documents, func(i, j int) bool { return snapshot.Documents[i].ID < snapshot.Documents[j].ID })
	sort.Slice(snapshot.Chunks, func(i, j int) bool { return snapshot.Chunks[i].ID < snapshot.Chunks[j].ID })
	return snapshot, nil
}

func isOKFReserved(name string) bool {
	return strings.EqualFold(name, "index.md") || strings.EqualFold(name, "log.md")
}

func unsafeRelativePath(value string) bool {
	return value == "." || value == ".." || strings.HasPrefix(value, "../") || path.IsAbs(value)
}

func parseOKFConcept(contents []byte) (Metadata, string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 64*1024), maxMarkdownBytes)
	if !scanner.Scan() || strings.TrimSuffix(scanner.Text(), "\r") != "---" {
		return Metadata{}, "", fmt.Errorf("%w: OKF concept must start with YAML frontmatter", ErrMalformedMarkdown)
	}
	var header, body strings.Builder
	closed := false
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "---" {
			closed = true
			break
		}
		header.WriteString(line)
		header.WriteByte('\n')
	}
	if !closed {
		return Metadata{}, "", fmt.Errorf("%w: unterminated YAML frontmatter", ErrMalformedMarkdown)
	}
	for scanner.Scan() {
		body.WriteString(strings.TrimSuffix(scanner.Text(), "\r"))
		body.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return Metadata{}, "", err
	}

	var metadata Metadata
	if err := yaml.Unmarshal([]byte(header.String()), &metadata); err != nil {
		return Metadata{}, "", fmt.Errorf("%w: invalid YAML frontmatter: %v", ErrMalformedMarkdown, err)
	}
	metadata.Type = strings.TrimSpace(metadata.Type)
	if metadata.Type == "" {
		return Metadata{}, "", fmt.Errorf("%w: OKF frontmatter requires non-empty type", ErrMalformedMarkdown)
	}
	if metadata.Status == "" {
		metadata.Status = "stable"
	}
	if metadata.Status != "draft" && metadata.Status != "stable" && metadata.Status != "deprecated" {
		return Metadata{}, "", fmt.Errorf("%w: invalid OKF status %q", ErrMalformedMarkdown, metadata.Status)
	}
	if metadata.Generated != nil && strings.TrimSpace(metadata.Generated.By) == "" {
		return Metadata{}, "", fmt.Errorf("%w: generated.by is required when generated is present", ErrMalformedMarkdown)
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(header.String()), &raw); err == nil {
		for _, key := range []string{"type", "title", "description", "resource", "tags", "sources", "usage_window", "generated", "verified", "status", "stale_after", "timestamp"} {
			delete(raw, key)
		}
		if len(raw) > 0 {
			metadata.Extra = raw
		}
	}
	return metadata, body.String(), nil
}

func resolveOKFLinks(sourcePath, body string, documents map[string]string) []Link {
	matches := markdownLinkPattern.FindAllStringSubmatchIndex(body, -1)
	links := make([]Link, 0, len(matches))
	for _, match := range matches {
		if len(match) < 6 || (match[0] > 0 && body[match[0]-1] == '!') {
			continue
		}
		text := body[match[2]:match[3]]
		rawTarget := strings.Trim(body[match[4]:match[5]], "<>")
		parsed, err := url.Parse(rawTarget)
		if err != nil || parsed.Scheme != "" || parsed.Host != "" || rawTarget == "" || strings.HasPrefix(rawTarget, "#") {
			continue
		}
		target := parsed.Path
		if unescaped, err := url.PathUnescape(target); err == nil {
			target = unescaped
		}
		var resolvedPath string
		if strings.HasPrefix(target, "/") {
			resolvedPath = path.Clean(strings.TrimPrefix(target, "/"))
		} else {
			resolvedPath = path.Clean(path.Join(path.Dir(sourcePath), target))
		}
		link := Link{Text: text, TargetPath: resolvedPath}
		if !unsafeRelativePath(resolvedPath) {
			if documentID, ok := documents[resolvedPath]; ok {
				link.TargetDocumentID = documentID
				link.Resolved = true
			}
		}
		links = append(links, link)
	}
	return links
}
