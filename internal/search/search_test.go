package search

import (
	"fmt"
	"testing"
)

type fakeEmbedder struct{}

func (fakeEmbedder) Embed(q string) ([]float32, error) {
	if q == "planning" {
		return []float32{1, 0}, nil
	}
	return []float32{0, 1}, nil
}

type failingEmbedder struct{}

func (failingEmbedder) Embed(string) ([]float32, error) {
	return nil, fmt.Errorf("provider unavailable")
}

func TestSearchFusesLexicalAndDenseRanks(t *testing.T) {
	idx, err := New(fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Add(Document{ID: "plan", Name: "plan", Description: "Create implementation plans", Vector: []float32{1, 0}, Searchable: true}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add(Document{ID: "review", Name: "review", Description: "Review changes", Vector: []float32{0, 1}, Searchable: true}); err != nil {
		t.Fatal(err)
	}
	hits, degraded, err := idx.Search("planning", 50, 50, 5, 60)
	if err != nil {
		t.Fatal(err)
	}
	if degraded || len(hits) == 0 || hits[0].ID != "plan" {
		t.Fatalf("hits=%+v degraded=%v", hits, degraded)
	}
}
func TestSearchReportsDegradedVectorWithoutEmbedder(t *testing.T) {
	idx, _ := New(nil)
	_ = idx.Add(Document{ID: "plan", Name: "plan", Description: "Create implementation plans", Searchable: true})
	hits, degraded, err := idx.Search("plans", 50, 50, 5, 60)
	if err != nil {
		t.Fatal(err)
	}
	if !degraded || len(hits) != 1 {
		t.Fatalf("hits=%+v degraded=%v", hits, degraded)
	}
}
func TestSearchRejectsEmptyQuery(t *testing.T) {
	idx, _ := New(nil)
	if _, _, err := idx.Search("", 50, 50, 5, 60); err == nil {
		t.Fatal("empty query accepted")
	}
}

func TestSearchAppliesPolicyFiltersBeforeReturningCandidates(t *testing.T) {
	idx, _ := New(nil)
	_ = idx.Add(Document{ID: "approved-plan", Name: "plan", Description: "implementation plan", RepositoryID: "approved-repo", TrustLevel: "approved", Metadata: map[string]string{"audience": "user"}, Searchable: true})
	_ = idx.Add(Document{ID: "internal-plan", Name: "plan", Description: "implementation plan", RepositoryID: "internal-repo", TrustLevel: "internal", Metadata: map[string]string{"audience": "workflow"}, Searchable: true})
	hits, _, err := idx.SearchWithFilters("implementation plan", 50, 50, 5, 60, Filters{Repositories: []string{"approved-repo"}, TrustLevels: []string{"approved"}, Metadata: map[string]string{"audience": "user"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "approved-plan" {
		t.Fatalf("filtered hits = %+v", hits)
	}
}

func TestIndexEmbedsRoutingDocumentsWhenVectorIsAbsent(t *testing.T) {
	idx, err := New(fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Add(Document{ID: "plan", Name: "plan", Description: "planning", Searchable: true}); err != nil {
		t.Fatal(err)
	}
	hits, degraded, err := idx.Search("planning", 50, 50, 1, 60)
	if err != nil {
		t.Fatal(err)
	}
	if degraded || len(hits) != 1 || hits[0].ID != "plan" {
		t.Fatalf("hits=%+v degraded=%v", hits, degraded)
	}
}

func TestSearchReportsDocumentEmbeddingDegradation(t *testing.T) {
	idx, err := New(failingEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Add(Document{ID: "plan", Name: "plan", Description: "planning", Searchable: true}); err != nil {
		t.Fatal(err)
	}
	_, degraded, err := idx.Search("planning", 50, 50, 1, 60)
	if err != nil {
		t.Fatal(err)
	}
	if !degraded {
		t.Fatal("document embedding failure was not reported")
	}
}

func TestEmbeddingObserverCountsIndexAndQueryProviderRequests(t *testing.T) {
	idx, err := New(fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	idx.SetEmbeddingObserver(func() { calls++ })
	if err := idx.Add(Document{ID: "org/repo/plan", Name: "plan", Description: "implementation plan", Searchable: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := idx.Search("plan", 5, 5, 1, 60); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("embedding observer calls = %d, want 2", calls)
	}
}
