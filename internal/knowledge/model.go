// Package knowledge owns organisational knowledge ingestion, persistence, and retrieval.
// It deliberately does not depend on skill or capability domain types.
package knowledge

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxMarkdownBytes = 4 << 20

var ErrMalformedMarkdown = errors.New("malformed markdown")

// Source describes a local source snapshot. Root may be a normal directory or
// a checked-out Git worktree; Revision records the caller-observed source
// revision (for example a commit SHA) without making Git a knowledge-domain
// dependency.
type Source struct {
	ID       string `json:"id"`
	Root     string `json:"root"`
	Locator  string `json:"locator"`
	Revision string `json:"revision"`
}

// Document is the authoritative knowledge-document identity and provenance.
// ID is stable across content revisions for the same source ID and relative path.
type Document struct {
	ID            string `json:"id"`
	SourceID      string `json:"source_id"`
	SourceLocator string `json:"source_locator"`
	SourceRevision string `json:"source_revision"`
	Path          string `json:"path"`
	ContentDigest string `json:"content_digest"`
}

// Chunk is one ATX-heading-aware knowledge section. Chunk identity changes
// when the section path or content revision changes, while DocumentID remains
// stable for edits to the same source path.
type Chunk struct {
	ID             string    `json:"id"`
	DocumentID     string    `json:"document_id"`
	SourceID       string    `json:"source_id"`
	SourceLocator  string    `json:"source_locator"`
	SourceRevision string    `json:"source_revision"`
	Path           string    `json:"path"`
	DocumentDigest string    `json:"document_digest"`
	Heading        string    `json:"heading,omitempty"`
	HeadingPath    []string  `json:"heading_path,omitempty"`
	Content        string    `json:"content"`
	ContentDigest  string    `json:"content_digest"`
	Vector         []float32 `json:"-"`
}

type section struct {
	path    []string
	content string
}

// Discover reads every Markdown file below the configured source roots.
// Discovery is deterministic and fails before publication when a source is
// unreadable, malformed, or aliases another source identity.
func Discover(sources []Source) ([]Document, []Chunk, error) {
	if len(sources) == 0 {
		return nil, nil, nil
	}
	ordered := append([]Source(nil), sources...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	seenSources := make(map[string]struct{}, len(ordered))
	seenDocs := map[string]string{}
	documents := []Document{}
	chunks := []Chunk{}

	for _, source := range ordered {
		source.ID = strings.TrimSpace(source.ID)
		if source.ID == "" {
			return nil, nil, errors.New("knowledge source id is required")
		}
		if _, exists := seenSources[source.ID]; exists {
			return nil, nil, fmt.Errorf("duplicate knowledge source id %q", source.ID)
		}
		seenSources[source.ID] = struct{}{}
		if strings.TrimSpace(source.Root) == "" {
			return nil, nil, fmt.Errorf("knowledge source %q root is required", source.ID)
		}
		root, err := filepath.Abs(source.Root)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve knowledge source %q: %w", source.ID, err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, nil, fmt.Errorf("read knowledge source %q: %w", source.ID, err)
		}
		if !info.IsDir() {
			return nil, nil, fmt.Errorf("knowledge source %q root is not a directory", source.ID)
		}
		locator := strings.TrimSpace(source.Locator)
		if locator == "" {
			locator = "file://" + filepath.ToSlash(root)
		}

		var paths []string
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != root && entry.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("discover knowledge source %q: %w", source.ID, err)
		}
		sort.Strings(paths)

		for _, path := range paths {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return nil, nil, fmt.Errorf("relativize knowledge path %q: %w", path, err)
			}
			relative = filepath.ToSlash(filepath.Clean(relative))
			contents, err := os.ReadFile(path)
			if err != nil {
				return nil, nil, fmt.Errorf("read knowledge document %s/%s: %w", source.ID, relative, err)
			}
			if len(contents) > maxMarkdownBytes {
				return nil, nil, fmt.Errorf("%w: %s/%s exceeds %d bytes", ErrMalformedMarkdown, source.ID, relative, maxMarkdownBytes)
			}
			if !utf8.Valid(contents) || bytes.IndexByte(contents, 0) >= 0 {
				return nil, nil, fmt.Errorf("%w: %s/%s is not valid UTF-8 text", ErrMalformedMarkdown, source.ID, relative)
			}
			parsed, err := parseATXSections(string(contents))
			if err != nil {
				return nil, nil, fmt.Errorf("parse knowledge document %s/%s: %w", source.ID, relative, err)
			}
			documentID := stableID("document", source.ID, relative)
			if previous, exists := seenDocs[documentID]; exists && previous != source.ID+"/"+relative {
				return nil, nil, fmt.Errorf("knowledge document id collision between %q and %q", previous, source.ID+"/"+relative)
			}
			seenDocs[documentID] = source.ID + "/" + relative
			documentDigest := digest(contents)
			document := Document{
				ID: documentID, SourceID: source.ID, SourceLocator: locator,
				SourceRevision: source.Revision, Path: relative, ContentDigest: documentDigest,
			}
			documents = append(documents, document)
			for _, parsedSection := range parsed {
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
				chunks = append(chunks, Chunk{
					ID: stableID("chunk", documentID, strings.Join(headingPath, "\x1f"), contentDigest),
					DocumentID: documentID, SourceID: source.ID, SourceLocator: locator,
					SourceRevision: source.Revision, Path: relative, DocumentDigest: documentDigest,
					Heading: heading, HeadingPath: headingPath, Content: content, ContentDigest: contentDigest,
				})
			}
		}
	}

	sort.Slice(documents, func(i, j int) bool { return documents[i].ID < documents[j].ID })
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].ID < chunks[j].ID })
	return documents, chunks, nil
}

func parseATXSections(markdown string) ([]section, error) {
	scanner := bufio.NewScanner(strings.NewReader(markdown))
	scanner.Buffer(make([]byte, 64*1024), maxMarkdownBytes)
	var sections []section
	var currentPath []string
	var body strings.Builder
	fence := byte(0)

	flush := func() {
		sections = append(sections, section{path: append([]string(nil), currentPath...), content: body.String()})
		body.Reset()
	}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if marker, ok := fenceMarker(trimmed); ok {
			if fence == 0 {
				fence = marker
			} else if marker == fence {
				fence = 0
			}
			body.WriteString(line)
			body.WriteByte('\n')
			continue
		}
		if fence == 0 {
			if level, title, ok := atxHeading(line); ok {
				flush()
				if level <= len(currentPath) {
					currentPath = currentPath[:level-1]
				}
				for len(currentPath) < level-1 {
					currentPath = append(currentPath, "")
				}
				currentPath = append(currentPath, title)
				continue
			}
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedMarkdown, err)
	}
	flush()
	return sections, nil
}

func atxHeading(line string) (int, string, bool) {
	if len(line) == 0 || line[0] != '#' {
		return 0, "", false
	}
	level := 0
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	if level == 0 || level < len(line) && line[level] != ' ' && line[level] != '\t' {
		return 0, "", false
	}
	title := strings.TrimSpace(line[level:])
	if title != "" {
		trimmed := strings.TrimRight(title, "#")
		if len(trimmed) < len(title) && strings.HasSuffix(trimmed, " ") {
			title = strings.TrimSpace(trimmed)
		}
	}
	return level, title, true
}

func fenceMarker(line string) (byte, bool) {
	if len(line) < 3 {
		return 0, false
	}
	marker := line[0]
	if marker != '`' && marker != '~' {
		return 0, false
	}
	if line[1] != marker || line[2] != marker {
		return 0, false
	}
	return marker, true
}

func stableID(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func digest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

func retrievalText(chunk Chunk) string {
	var out strings.Builder
	if len(chunk.HeadingPath) > 0 {
		out.WriteString("section: ")
		out.WriteString(strings.Join(chunk.HeadingPath, " > "))
		out.WriteByte('\n')
	}
	out.WriteString(chunk.Content)
	return out.String()
}
