package retrieval

import (
	"math"
	"reflect"
	"testing"
)

func TestLexicalIndexTreatsNaturalLanguageAsText(t *testing.T) {
	index, err := NewLexicalIndex()
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if err := index.Index("architecture", "name: architecture\ndescription: Map a repository\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Search("Explore /Users/example/skillet on macOS", 10); err != nil {
		t.Fatalf("natural-language path query returned an error: %v", err)
	}
}

func TestRankIDsUsesFirstDuplicateOccurrence(t *testing.T) {
	got := RankIDs([]string{"a", "b", "a"})
	want := []RankedID{{ID: "a", Rank: 1}, {ID: "b", Rank: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RankIDs() = %#v, want %#v", got, want)
	}
}

func TestReciprocalRankFusionUsesDeterministicTieBreak(t *testing.T) {
	got := ReciprocalRankFusion(60,
		[]RankedID{{ID: "b", Rank: 1}, {ID: "a", Rank: 2}},
		[]RankedID{{ID: "a", Rank: 1}, {ID: "b", Rank: 2}},
	)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("order = %v, want [a b]", []string{got[0].ID, got[1].ID})
	}
	if got[0].Score != got[1].Score {
		t.Fatalf("scores differ: %v vs %v", got[0].Score, got[1].Score)
	}
}

func TestReciprocalRankFusionCountsDuplicateIDOncePerSource(t *testing.T) {
	got := ReciprocalRankFusion(60,
		[]RankedID{{ID: "a", Rank: 5}, {ID: "a", Rank: 1}},
		[]RankedID{{ID: "a", Rank: 2}},
	)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Ranks[0] != 1 || got[0].Ranks[1] != 2 {
		t.Fatalf("ranks = %v, want [1 2]", got[0].Ranks)
	}
	want := 1.0/61.0 + 1.0/62.0
	if math.Abs(got[0].Score-want) > 1e-15 {
		t.Fatalf("score = %v, want %v", got[0].Score, want)
	}
}

func TestRankByCosineIsDeterministicForEqualScores(t *testing.T) {
	query := []float32{1, 0}
	orders := [][]VectorCandidate{
		{{ID: "c", Vector: []float32{0, 1}}, {ID: "a", Vector: []float32{0, 1}}, {ID: "b", Vector: []float32{0, 1}}},
		{{ID: "b", Vector: []float32{0, 1}}, {ID: "c", Vector: []float32{0, 1}}, {ID: "a", Vector: []float32{0, 1}}},
	}
	for _, candidates := range orders {
		got := RankByCosine(query, candidates, 10)
		if len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
			t.Fatalf("order = %#v, want a,b,c", got)
		}
	}
}

func TestRankByCosineKeepsBestDuplicateID(t *testing.T) {
	got := RankByCosine([]float32{1, 0}, []VectorCandidate{
		{ID: "a", Vector: []float32{0, 1}},
		{ID: "a", Vector: []float32{1, 0}},
		{ID: "b", Vector: []float32{0, 1}},
	}, 10)
	if len(got) != 2 || got[0].ID != "a" || got[0].Score != 1 {
		t.Fatalf("results = %#v", got)
	}
}

func TestRankByCosineHandlesEmptyAndMismatchedVectors(t *testing.T) {
	if got := RankByCosine(nil, nil, 10); len(got) != 0 {
		t.Fatalf("empty results = %#v", got)
	}
	got := RankByCosine([]float32{1, 0}, []VectorCandidate{
		{ID: "b", Vector: nil},
		{ID: "a", Vector: []float32{1}},
	}, 10)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" || got[0].Score != 0 || got[1].Score != 0 {
		t.Fatalf("mismatched results = %#v", got)
	}
}

func TestDiagnosticsReportsDegradedMechanisms(t *testing.T) {
	if (Diagnostics{}).Degraded() {
		t.Fatal("zero diagnostics reported degradation")
	}
	if !(Diagnostics{VectorDegraded: true}).Degraded() {
		t.Fatal("vector degradation was not reported")
	}
	if !(Diagnostics{RerankerDegraded: true}).Degraded() {
		t.Fatal("reranker degradation was not reported")
	}
}

type rerankItem struct {
	id     string
	reason string
}

func TestApplyRerankRequiresExactPermutation(t *testing.T) {
	items := []rerankItem{{id: "a"}, {id: "b"}}
	ordered := []RerankResult{{ID: "b", Reason: "best"}, {ID: "a", Reason: "second"}}
	got, err := ApplyRerank(items, ordered,
		func(item rerankItem) string { return item.id },
		func(item rerankItem, reason string) rerankItem { item.reason = reason; return item },
	)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].id != "b" || got[0].reason != "best" || got[1].id != "a" || got[1].reason != "second" {
		t.Fatalf("reranked = %#v", got)
	}
}

func TestApplyRerankRejectsDuplicateItemIDs(t *testing.T) {
	_, err := ApplyRerank(
		[]rerankItem{{id: "a"}, {id: "a"}},
		[]RerankResult{{ID: "a"}, {ID: "a"}},
		func(item rerankItem) string { return item.id },
		nil,
	)
	if err == nil {
		t.Fatal("duplicate item IDs were accepted")
	}
}

func TestApplyRerankRejectsDuplicateResultIDs(t *testing.T) {
	_, err := ApplyRerank(
		[]rerankItem{{id: "a"}, {id: "b"}},
		[]RerankResult{{ID: "a"}, {ID: "a"}},
		func(item rerankItem) string { return item.id },
		nil,
	)
	if err == nil {
		t.Fatal("duplicate rerank result IDs were accepted")
	}
}
