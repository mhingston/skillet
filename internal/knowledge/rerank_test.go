package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/retrieval"
)

type preferSecondReranker struct{}

func (preferSecondReranker) Rerank(_ context.Context, _ string, candidates []Candidate) ([]retrieval.RerankResult, error) {
	ordered := make([]retrieval.RerankResult, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Path == "second.md" {
			ordered = append(ordered, retrieval.RerankResult{ID: candidate.ID, Reason: "preferred fixture"})
		}
	}
	for _, candidate := range candidates {
		if candidate.Path != "second.md" {
			ordered = append(ordered, retrieval.RerankResult{ID: candidate.ID, Reason: "remaining fixture"})
		}
	}
	return ordered, nil
}

func TestKnowledgeSearchCanApplyGenericReranker(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for name, body := range map[string]string{
		"first.md":  "# First\n\nShared persimmon marker.\n",
		"second.md": "# Second\n\nShared persimmon marker.\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service, err := Open(ctx, filepath.Join(t.TempDir(), "data"), Options{Reranker: preferSecondReranker{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.Reindex(ctx, []Source{{ID: "fixtures", Root: root}}); err != nil {
		t.Fatal(err)
	}
	response, err := service.Search(ctx, "persimmon marker", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) < 2 || response.Results[0].Path != "second.md" || response.Results[0].RerankReason != "preferred fixture" {
		t.Fatalf("reranked results = %+v", response.Results)
	}
	if response.Diagnostics.RerankerDegraded {
		t.Fatal("successful reranker was reported degraded")
	}
}
