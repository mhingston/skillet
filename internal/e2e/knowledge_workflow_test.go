package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/knowledge"
	"github.com/mhingston/skillet/internal/knowledgecli"
)

func TestOfflineKnowledgeIndexSearchReadAndReindex(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	sourceDir := filepath.Join(root, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("data directory should begin absent, stat error=%v", err)
	}

	handbookPath := filepath.Join(sourceDir, "handbook.md")
	stalePath := filepath.Join(sourceDir, "stale.md")
	if err := os.WriteFile(handbookPath, []byte("# Operations Handbook\n\n## Alpha procedure\n\nUse the cobalt recovery marker for the stable procedure.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stalePath, []byte("# Old Policy\n\n## Narwhal rule\n\nThe obsolete narwhal rule should later disappear.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	indexKnowledge(t, ctx, dataDir, sourceDir, "commit-v1")
	alpha := searchKnowledge(t, ctx, dataDir, "cobalt recovery marker")
	if len(alpha.Results) == 0 {
		t.Fatal("initial alpha knowledge search returned no results")
	}
	first := alpha.Results[0]
	if first.Path != "handbook.md" || first.Heading != "Alpha procedure" || first.SourceID != "handbook" || first.SourceLocator != "git://example/handbook" || first.SourceRevision != "commit-v1" {
		t.Fatalf("initial provenance = %+v", first)
	}
	stableDocumentID := first.DocumentID
	stableChunkID := first.ChunkID
	firstDocumentDigest := first.DocumentDigest
	stale := searchKnowledge(t, ctx, dataDir, "obsolete narwhal rule")
	if len(stale.Results) == 0 {
		t.Fatal("initial stale document was not indexed")
	}
	staleChunkID := stale.Results[0].ChunkID

	var read bytes.Buffer
	if err := knowledgecli.Run(ctx, []string{"read", "-data-dir", dataDir, "-chunk-id", stableChunkID}, &read, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var full knowledge.Chunk
	if err := json.Unmarshal(read.Bytes(), &full); err != nil {
		t.Fatal(err)
	}
	if full.ID != stableChunkID || full.Content != "Use the cobalt recovery marker for the stable procedure." {
		t.Fatalf("read chunk = %+v", full)
	}

	if err := os.WriteFile(handbookPath, []byte("# Operations Handbook\n\n## Alpha procedure\n\nUse the cobalt recovery marker for the stable procedure.\n\n## Beta procedure\n\nUse the violet addition for the new procedure.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stalePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "new.md"), []byte("# New Handbook Page\n\n## Octopus procedure\n\nThe silver octopus addition proves new documents are indexed.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	indexKnowledge(t, ctx, dataDir, sourceDir, "commit-v2")
	alpha2 := searchKnowledge(t, ctx, dataDir, "cobalt recovery marker")
	if len(alpha2.Results) == 0 {
		t.Fatal("stable alpha section disappeared after reindex")
	}
	updated := alpha2.Results[0]
	if updated.DocumentID != stableDocumentID {
		t.Fatalf("document identity changed: %s -> %s", stableDocumentID, updated.DocumentID)
	}
	if updated.ChunkID != stableChunkID {
		t.Fatalf("unchanged section identity changed: %s -> %s", stableChunkID, updated.ChunkID)
	}
	if updated.DocumentDigest == firstDocumentDigest {
		t.Fatal("document revision digest did not change after adding a section")
	}
	if updated.SourceRevision != "commit-v2" {
		t.Fatalf("updated source revision = %q", updated.SourceRevision)
	}
	if len(searchKnowledge(t, ctx, dataDir, "obsolete narwhal rule").Results) != 0 {
		t.Fatal("deleted document remained searchable after reindex")
	}
	if len(searchKnowledge(t, ctx, dataDir, "violet addition").Results) == 0 {
		t.Fatal("new section was not indexed")
	}
	newDocument := searchKnowledge(t, ctx, dataDir, "silver octopus addition")
	if len(newDocument.Results) == 0 || newDocument.Results[0].Path != "new.md" {
		t.Fatalf("new document results = %+v", newDocument.Results)
	}

	var ignored bytes.Buffer
	err := knowledgecli.Run(ctx, []string{"read", "-data-dir", dataDir, "-chunk-id", staleChunkID}, &ignored, &bytes.Buffer{})
	if !errors.Is(err, knowledge.ErrChunkNotFound) {
		t.Fatalf("reading deleted stale chunk error = %v", err)
	}
}

func indexKnowledge(t *testing.T, ctx context.Context, dataDir, sourceDir, revision string) {
	t.Helper()
	var output bytes.Buffer
	var stderr bytes.Buffer
	err := knowledgecli.Run(ctx, []string{
		"index", "-data-dir", dataDir,
		"-source-root", sourceDir,
		"-source-id", "handbook",
		"-locator", "git://example/handbook",
		"-revision", revision,
	}, &output, &stderr)
	if err != nil {
		t.Fatalf("knowledge index: %v stderr=%s", err, stderr.String())
	}
	var stats knowledge.Stats
	if err := json.Unmarshal(output.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Documents == 0 || stats.Chunks == 0 {
		t.Fatalf("knowledge index stats = %+v", stats)
	}
}

func searchKnowledge(t *testing.T, ctx context.Context, dataDir, query string) knowledge.SearchResponse {
	t.Helper()
	var output bytes.Buffer
	var stderr bytes.Buffer
	if err := knowledgecli.Run(ctx, []string{"search", "-data-dir", dataDir, "-query", query, "-limit", "5"}, &output, &stderr); err != nil {
		t.Fatalf("knowledge search %q: %v stderr=%s", query, err, stderr.String())
	}
	var response knowledge.SearchResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}
