package knowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type failingEmbedder struct{}

func (failingEmbedder) Embed(string) ([]float32, error) { return nil, errors.New("embedding unavailable") }

func TestDiscoverUsesStableDocumentAndSectionRevisionIdentity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "runbook.md")
	first := "# Operations Runbook\n\nGeneral recovery guidance.\n\n```text\n## Not a heading\n```\n\n## Rollback\n\nRevert the latest deployment.\n"
	if err := os.WriteFile(path, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	source := Source{ID: "ops", Root: root, Locator: "git://example/ops", Revision: "commit-1"}
	documents, chunks, err := Discover([]Source{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 || len(chunks) != 2 {
		t.Fatalf("documents=%d chunks=%d, want 1 and 2", len(documents), len(chunks))
	}
	if chunks[0].DocumentID != documents[0].ID || chunks[1].DocumentID != documents[0].ID {
		t.Fatal("chunks do not preserve stable document identity")
	}
	byHeading := map[string]Chunk{}
	for _, chunk := range chunks {
		byHeading[chunk.Heading] = chunk
	}
	rollback := byHeading["Rollback"]
	general := byHeading["Operations Runbook"]
	if rollback.ID == "" || general.ID == "" {
		t.Fatalf("heading-aware chunks = %+v", chunks)
	}
	if len(rollback.HeadingPath) != 2 || rollback.HeadingPath[0] != "Operations Runbook" || rollback.HeadingPath[1] != "Rollback" {
		t.Fatalf("rollback heading path = %#v", rollback.HeadingPath)
	}
	if general.Content == "" || !containsText(general.Content, "## Not a heading") {
		t.Fatalf("fenced heading was incorrectly parsed: %q", general.Content)
	}

	second := "# Operations Runbook\n\nGeneral recovery guidance.\n\n```text\n## Not a heading\n```\n\n## Rollback\n\nRestore the previously verified release.\n"
	if err := os.WriteFile(path, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	documents2, chunks2, err := Discover([]Source{{ID: "ops", Root: root, Locator: "git://example/ops", Revision: "commit-2"}})
	if err != nil {
		t.Fatal(err)
	}
	if documents2[0].ID != documents[0].ID {
		t.Fatalf("document id changed across content revision: %s -> %s", documents[0].ID, documents2[0].ID)
	}
	byHeading2 := map[string]Chunk{}
	for _, chunk := range chunks2 {
		byHeading2[chunk.Heading] = chunk
	}
	if byHeading2["Operations Runbook"].ID != general.ID {
		t.Fatal("unchanged section identity changed")
	}
	if byHeading2["Rollback"].ID == rollback.ID {
		t.Fatal("changed section retained stale chunk identity")
	}
	if documents2[0].ContentDigest == documents[0].ContentDigest {
		t.Fatal("document content digest did not change")
	}
}

func TestDiscoverRejectsDuplicateSourceIdentity(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.md"), []byte("# One\n\nContent.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Discover([]Source{{ID: "same", Root: root}, {ID: "same", Root: root}})
	if err == nil {
		t.Fatal("duplicate source IDs were accepted")
	}
}

func TestFailedReindexPreservesPreviousAuthoritativeSnapshot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "runbook.md")
	if err := os.WriteFile(path, []byte("# Rollback\n\nUse the cobalt rollback marker.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service, err := Open(ctx, filepath.Join(t.TempDir(), "data"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.Reindex(ctx, []Source{{ID: "ops", Root: root, Revision: "one"}}); err != nil {
		t.Fatal(err)
	}
	before, err := service.Search(ctx, "cobalt rollback marker", 5)
	if err != nil || len(before.Results) == 0 {
		t.Fatalf("initial search err=%v results=%+v", err, before.Results)
	}
	oldChunk := before.Results[0].ChunkID

	if err := os.WriteFile(path, []byte("# Broken\n\x00invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reindex(ctx, []Source{{ID: "ops", Root: root, Revision: "two"}}); !errors.Is(err, ErrMalformedMarkdown) {
		t.Fatalf("malformed reindex error = %v", err)
	}
	after, err := service.Search(ctx, "cobalt rollback marker", 5)
	if err != nil || len(after.Results) == 0 || after.Results[0].ChunkID != oldChunk {
		t.Fatalf("failed reindex changed authoritative search state: err=%v results=%+v", err, after.Results)
	}
}

func TestCancelledPublicationDoesNotReplaceCatalogue(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "guide.md")
	if err := os.WriteFile(path, []byte("# Guide\n\nOld amber procedure.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	service, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.Reindex(ctx, []Source{{ID: "guide", Root: root}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Guide\n\nNew violet procedure.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.Reindex(cancelled, []Source{{ID: "guide", Root: root}}); err == nil {
		t.Fatal("cancelled reindex unexpectedly committed")
	}
	oldResults, err := service.Search(ctx, "amber procedure", 5)
	if err != nil || len(oldResults.Results) == 0 {
		t.Fatalf("old snapshot disappeared after cancelled publication: err=%v results=%+v", err, oldResults.Results)
	}
	newResults, err := service.Search(ctx, "violet", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(newResults.Results) != 0 {
		t.Fatalf("cancelled snapshot became visible: %+v", newResults.Results)
	}
}

func TestEmbeddingFailureFallsBackToLexicalWithDiagnostics(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "policy.md"), []byte("# Escalation\n\nUse the marmalade escalation phrase.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service, err := Open(ctx, filepath.Join(t.TempDir(), "data"), Options{Embedder: failingEmbedder{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	stats, err := service.Reindex(ctx, []Source{{ID: "policy", Root: root}})
	if err != nil {
		t.Fatal(err)
	}
	if !stats.VectorDegraded {
		t.Fatal("index-time embedding failure was not reported")
	}
	results, err := service.Search(ctx, "marmalade escalation phrase", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Results) == 0 || results.Results[0].LexicalRank == 0 {
		t.Fatalf("lexical fallback results = %+v", results.Results)
	}
	if !results.Diagnostics.VectorDegraded {
		t.Fatal("query did not report vector degradation")
	}
}

func containsText(value, target string) bool {
	for i := 0; i+len(target) <= len(value); i++ {
		if value[i:i+len(target)] == target {
			return true
		}
	}
	return false
}
