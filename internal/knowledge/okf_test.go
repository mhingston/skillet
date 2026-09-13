package knowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverOKFPreservesMetadataAndExplicitLinks(t *testing.T) {
	snapshot, err := DiscoverOKF([]OKFBundle{{ID: "fixture", Root: filepath.Join("testdata", "okf-v02"), Locator: "git://fixture", Revision: "abc123"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Documents) != 4 {
		t.Fatalf("documents = %d, want 4 (reserved index.md must not be a concept)", len(snapshot.Documents))
	}
	var overview OKFDocument
	for _, detail := range snapshot.Details {
		if detail.Metadata.Title == "Retention coaching overview" {
			overview = detail
			break
		}
	}
	if overview.Document.ID == "" {
		t.Fatal("overview not discovered")
	}
	if overview.Document.SourceLocator != "git://fixture" || overview.Document.SourceRevision != "abc123" {
		t.Fatalf("source provenance = %+v", overview.Document)
	}
	if len(overview.Metadata.Sources) != 1 || overview.Metadata.Sources[0].ID != "ops-handbook" {
		t.Fatalf("OKF provenance = %+v", overview.Metadata.Sources)
	}
	if len(overview.Metadata.Verified) != 1 || overview.Metadata.Verified[0].By != "human:reviewer" {
		t.Fatalf("bare verified mapping was not normalized: %+v", overview.Metadata.Verified)
	}
	if overview.Metadata.Extra["owner_team"] != "retention-platform" {
		t.Fatalf("producer metadata not preserved: %+v", overview.Metadata.Extra)
	}
	resolved, unresolved := 0, 0
	for _, link := range overview.Links {
		if link.Resolved {
			resolved++
		} else if link.TargetPath == "missing.md" {
			unresolved++
		}
	}
	if resolved != 2 || unresolved != 1 {
		t.Fatalf("links = %+v", overview.Links)
	}
}

func TestKnowledgeProjectionToleratesPlainMarkdown(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeOKFTestFile(t, root, "plain.md", "# Plain knowledge\n\nplain-compatibility-token\n")
	service, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.Reindex(ctx, []Source{{ID: "plain", Root: root, Locator: "file://plain", Revision: "r1"}}); err != nil {
		t.Fatal(err)
	}

	search, err := service.SearchOKF(ctx, "plain-compatibility-token", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 {
		t.Fatalf("results = %+v, want one plain Markdown result", search.Results)
	}
	result := search.Results[0]
	if result.Metadata.Type != "" || result.Result.SourceRevision != "r1" {
		t.Fatalf("plain Markdown projection = %+v", result)
	}
	read, err := service.ReadOKF(ctx, result.Result.ChunkID)
	if err != nil {
		t.Fatal(err)
	}
	if read.Metadata.Type != "" || len(read.Links) != 0 || !strings.Contains(read.Chunk.Content, "plain-compatibility-token") {
		t.Fatalf("plain Markdown read = %+v", read)
	}
	backlinks, err := service.GetBacklinks(ctx, result.Result.DocumentID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(backlinks) != 0 {
		t.Fatalf("plain Markdown backlinks = %+v, want none", backlinks)
	}
}

func TestDiscoverOKFRejectsMalformedMetadata(t *testing.T) {
	root := t.TempDir()
	writeOKFTestFile(t, root, "bad.md", "---\ntitle: missing type\n---\n\n# Bad\n")
	_, err := DiscoverOKF([]OKFBundle{{ID: "bad", Root: root}})
	if !errors.Is(err, ErrMalformedMarkdown) {
		t.Fatalf("error = %v, want ErrMalformedMarkdown", err)
	}
}

func TestDiscoverOKFRejectsCaseFoldedIdentityCollision(t *testing.T) {
	root := t.TempDir()
	content := "---\ntype: Reference\n---\n\n# Body\n"
	writeOKFTestFile(t, root, "Policy.md", content)
	writeOKFTestFile(t, root, "policy.md", content)
	_, err := DiscoverOKF([]OKFBundle{{ID: "collision", Root: root}})
	if err == nil || !strings.Contains(err.Error(), "identity collision") {
		t.Fatalf("error = %v, want identity collision", err)
	}
}

func TestDiscoverOKFRejectsSymlinkBundlePath(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("---\ntype: Reference\n---\n\n# Outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := DiscoverOKF([]OKFBundle{{ID: "unsafe", Root: root}})
	if err == nil || !strings.Contains(err.Error(), "symbolic links are not allowed") {
		t.Fatalf("error = %v, want unsafe symlink rejection", err)
	}
}

func TestDiscoverOKFRejectsOverlargeConcept(t *testing.T) {
	root := t.TempDir()
	contents := "---\ntype: Reference\n---\n\n" + strings.Repeat("x", maxMarkdownBytes)
	writeOKFTestFile(t, root, "large.md", contents)
	_, err := DiscoverOKF([]OKFBundle{{ID: "large", Root: root}})
	if !errors.Is(err, ErrMalformedMarkdown) {
		t.Fatalf("error = %v, want ErrMalformedMarkdown", err)
	}
}

func TestResolveOKFLinksDoesNotPromoteTraversalOrExternalLinks(t *testing.T) {
	documents := map[string]string{"safe.md": "safe-id"}
	links := resolveOKFLinks("folder/source.md", "[escape](../../outside.md) [web](https://example.com) [safe](/safe.md)", documents)
	if len(links) != 2 {
		t.Fatalf("links = %+v, want traversal + safe only", links)
	}
	if links[0].Resolved || links[0].TargetDocumentID != "" {
		t.Fatalf("traversal link became authoritative: %+v", links[0])
	}
	if !links[1].Resolved || links[1].TargetDocumentID != "safe-id" {
		t.Fatalf("safe explicit link not resolved: %+v", links[1])
	}
}

func writeOKFTestFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
