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

// SourceReference preserves OKF provenance without assigning a trust score.
type SourceReference struct {
	ID           string     `json:"id,omitempty" yaml:"id,omitempty"`
	Resource     string     `json:"resource,omitempty" yaml:"resource,omitempty"`
	Title        string     `json:"title,omitempty" yaml:"title,omitempty"`
	Author       string     `json:"author,omitempty" yaml:"author,omitempty"`
	UsageCount   *int64     `json:"usage_count,omitempty" yaml:"usage_count,omitempty"`
	LastModified string     `json:"last_modified,omitempty" yaml:"last_modified,omitempty"`
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

// Verifications accepts both the OKF list form and the required one-element
// mapping shorthand.
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

// Link is one explicit Markdown link. Resolved links carry the authoritative
// target document ID; unresolved links are retained as evidence but never
// promoted into graph semantics.
type Link struct {
	Text             string `json:"text,omitempty"`
	TargetPath       string `json:"target_path"`
	TargetDocumentID string `json:"target_document_id,omitempty"`
	Resolved         bool   `json:"resolved"`
}

// Backlink is a compact projection of an explicit incoming Markdown link.
type Backlink struct {
	DocumentID string `json:"document_id"`
	Path       string `json:"path"`
	Title      string `json:"title,omitempty"`
	LinkText   string `json:"link_text,omitempty"`
}

type okfParsedDocument struct {
	document Document
	body     string
}

var markdownLinkPattern = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)(?:\s+["'][^"']*["'])?\)`)

// DiscoverOKF validates and maps local OKF bundles into the existing knowledge
// document/chunk model. It is deterministic and performs no network access.
func DiscoverOKF(bundles []OKFBundle) ([]Document, []Chunk, error) {
	if len(bundles) == 0 {
		return nil, nil, nil
	}
	ordered := append([]OKFBundle(nil), bundles...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	seenBundleIDs := map[string]struct{}{}
	seenDocumentIDs := map[string]string{}
	allDocuments := []Document{}
	allChunks := []Chunk{}

	for _, bundle := range ordered {
		bundle.ID = strings.TrimSpace(bundle.ID)
		if bundle.ID == "" {
			return nil, nil, errors.New("OKF bundle id is required")
		}
		if _, exists := seenBundleIDs[bundle.ID]; exists {
			return nil, nil, fmt.Errorf("duplicate OKF bundle id %q", bundle.ID)
		}
		seenBundleIDs[bundle.ID] = struct{}{}
		if strings.TrimSpace(bundle.Root) == "" {
			return nil, nil, fmt.Errorf("OKF bundle %q root is required", bundle.ID)
		}
		root, err := filepath.Abs(bundle.Root)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve OKF bundle %q: %w", bundle.ID, err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, nil, fmt.Errorf("read OKF bundle %q: %w", bundle.ID, err)
		}
		if !info.IsDir() {
			return nil, nil, fmt.Errorf("OKF bundle %q root is not a directory", bundle.ID)
		}
		locator := strings.TrimSpace(bundle.Locator)
		if locator == "" {
			locator = "file://" + filepath.ToSlash(root)
		}

		var paths []string
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
				paths = append(paths, current)
			}
			return nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("discover OKF bundle %q: %w", bundle.ID, err)
		}
		sort.Strings(paths)

		parsed := make([]okfParsedDocument, 0, len(paths))
		byPath := make(map[string]string, len(paths))
		caseFolded := make(map[string]string, len(paths))
		for _, filePath := range paths {
			relative, err := filepath.Rel(root, filePath)
			if err != nil {
				return nil, nil, fmt.Errorf("relativize OKF path %q: %w", filePath, err)
			}
			relative = filepath.ToSlash(filepath.Clean(relative))
			if relative == "." || relative == ".." || strings.HasPrefix(relative, "../") || path.IsAbs(relative) {
				return nil, nil, fmt.Errorf("unsafe OKF bundle path %q", relative)
			}
			conceptID := strings.TrimSuffix(relative, path.Ext(relative))
			folded := strings.ToLower(conceptID)
			if previous, exists := caseFolded[folded]; exists && previous != conceptID {
				return nil, nil, fmt.Errorf("duplicate OKF concept identity collision between %q and %q", previous, conceptID)
			}
			caseFolded[folded] = conceptID

			contents, err := os.ReadFile(filePath)
			if err != nil {
				return nil, nil, fmt.Errorf("read OKF concept %s/%s: %w", bundle.ID, relative, err)
			}
			if len(contents) > maxMarkdownBytes {
				return nil, nil, fmt.Errorf("%w: %s/%s exceeds %d bytes", ErrMalformedMarkdown, bundle.ID, relative, maxMarkdownBytes)
			}
			if !utf8.Valid(contents) || bytes.IndexByte(contents, 0) >= 0 {
				return nil, nil, fmt.Errorf("%w: %s/%s is not valid UTF-8 text", ErrMalformedMarkdown, bundle.ID, relative)
			}
			metadata, body, err := parseOKFConcept(contents)
			if err != nil {
				return nil, nil, fmt.Errorf("parse OKF concept %s/%s: %w", bundle.ID, relative, err)
			}
			documentID := stableID("document", bundle.ID, relative)
			identity := bundle.ID + "/" + relative
			if previous, exists := seenDocumentIDs[documentID]; exists && previous != identity {
				return nil, nil, fmt.Errorf("knowledge document id collision between %q and %q", previous, identity)
			}
			seenDocumentIDs[documentID] = identity
			document := Document{
				ID: documentID, SourceID: bundle.ID, SourceLocator: locator,
				SourceRevision: bundle.Revision, Path: relative, ContentDigest: digest(contents),
				Metadata: metadata,
			}
			parsed = append(parsed, okfParsedDocument{document: document, body: body})
			byPath[relative] = documentID
		}

		for n := range parsed {
			parsed[n].document.Links = resolveOKFLinks(parsed[n].document.Path, parsed[n].body, byPath)
			sections, err := parseATXSections(parsed[n].body)
			if err != nil {
				return nil, nil, fmt.Errorf("parse OKF concept body %s/%s: %w", bundle.ID, parsed[n].document.Path, err)
			}
			allDocuments = append(allDocuments, parsed[n].document)
			for _, parsedSection := range sections {
				content := strings.TrimSpace(parsedSection.content)
				if content == "" && len(parsedSection.path) == 0 {
					continue
				}
				contentDigest := digest([]byte(content))
				headingPath := append([]string(nil), parsedSection.path...)
				heading := ""
				if len(headingPath) > 0 {
					heading = headingPath[len(headingPath)-1]
				}
				allChunks = append(allChunks, Chunk{
					ID: stableID("chunk", parsed[n].document.ID, strings.Join(headingPath, "\x1f"), contentDigest),
					DocumentID: parsed[n].document.ID, SourceID: bundle.ID, SourceLocator: locator,
					SourceRevision: bundle.Revision, Path: parsed[n].document.Path,
					DocumentDigest: parsed[n].document.ContentDigest, Heading: heading,
					HeadingPath: headingPath, Content: content, ContentDigest: contentDigest,
					Metadata: cloneMetadata(parsed[n].document.Metadata), Links: cloneLinks(parsed[n].document.Links),
				})
			}
		}
	}

	sort.Slice(allDocuments, func(i, j int) bool { return allDocuments[i].ID < allDocuments[j].ID })
	sort.Slice(allChunks, func(i, j int) bool { return allChunks[i].ID < allChunks[j].ID })
	return allDocuments, allChunks, nil
}

func isOKFReserved(name string) bool {
	return strings.EqualFold(name, "index.md") || strings.EqualFold(name, "log.md")
}

func parseOKFConcept(contents []byte) (*Metadata, string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 64*1024), maxMarkdownBytes)
	if !scanner.Scan() || strings.TrimSuffix(scanner.Text(), "\r") != "---" {
		return nil, "", fmt.Errorf("%w: OKF concept must start with YAML frontmatter", ErrMalformedMarkdown)
	}
	var header strings.Builder
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
		return nil, "", fmt.Errorf("%w: unterminated YAML frontmatter", ErrMalformedMarkdown)
	}
	var body strings.Builder
	for scanner.Scan() {
		body.WriteString(strings.TrimSuffix(scanner.Text(), "\r"))
		body.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrMalformedMarkdown, err)
	}

	var metadata Metadata
	if err := yaml.Unmarshal([]byte(header.String()), &metadata); err != nil {
		return nil, "", fmt.Errorf("%w: invalid YAML frontmatter: %v", ErrMalformedMarkdown, err)
	}
	metadata.Type = strings.TrimSpace(metadata.Type)
	if metadata.Type == "" {
		return nil, "", fmt.Errorf("%w: OKF frontmatter requires non-empty type", ErrMalformedMarkdown)
	}
	if metadata.Status == "" {
		metadata.Status = "stable"
	}
	if metadata.Status != "draft" && metadata.Status != "stable" && metadata.Status != "deprecated" {
		return nil, "", fmt.Errorf("%w: invalid OKF status %q", ErrMalformedMarkdown, metadata.Status)
	}
	if metadata.Generated != nil && strings.TrimSpace(metadata.Generated.By) == "" {
		return nil, "", fmt.Errorf("%w: generated.by is required when generated is present", ErrMalformedMarkdown)
	}

	var raw map[string]any
	if err := yaml.Unmarshal([]byte(header.String()), &raw); err != nil {
		return nil, "", fmt.Errorf("%w: invalid YAML frontmatter: %v", ErrMalformedMarkdown, err)
	}
	for _, key := range []string{"type", "title", "description", "resource", "tags", "sources", "usage_window", "generated", "verified", "status", "stale_after", "timestamp"} {
		delete(raw, key)
	}
	if len(raw) > 0 {
		metadata.Extra = raw
	}
	return &metadata, body.String(), nil
}

func resolveOKFLinks(sourcePath, body string, documents map[string]string) []Link {
	matches := markdownLinkPattern.FindAllStringSubmatchIndex(body, -1)
	links := make([]Link, 0, len(matches))
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		start := match[0]
		if start > 0 && body[start-1] == '!' {
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
		if resolvedPath == "." || resolvedPath == ".." || strings.HasPrefix(resolvedPath, "../") || path.IsAbs(resolvedPath) {
			links = append(links, link)
			continue
		}
		if documentID, exists := documents[resolvedPath]; exists {
			link.TargetDocumentID = documentID
			link.Resolved = true
		}
		links = append(links, link)
	}
	return links
}

func cloneMetadata(metadata *Metadata) *Metadata {
	if metadata == nil {
		return nil
	}
	copyValue := *metadata
	copyValue.Tags = append([]string(nil), metadata.Tags...)
	copyValue.Sources = append([]SourceReference(nil), metadata.Sources...)
	copyValue.Verified = append(Verifications(nil), metadata.Verified...)
	if metadata.UsageWindow != nil {
		value := *metadata.UsageWindow
		copyValue.UsageWindow = &value
	}
	if metadata.Generated != nil {
		value := *metadata.Generated
		copyValue.Generated = &value
	}
	if metadata.Extra != nil {
		copyValue.Extra = make(map[string]any, len(metadata.Extra))
		for key, value := range metadata.Extra {
			copyValue.Extra[key] = value
		}
	}
	return &copyValue
}

func cloneLinks(links []Link) []Link {
	return append([]Link(nil), links...)
}
