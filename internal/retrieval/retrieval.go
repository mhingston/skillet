// Package retrieval contains domain-neutral retrieval mechanics shared by
// capability and knowledge retrieval. It deliberately owns no canonical
// capability, skill, document, or knowledge model.
package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/blevesearch/bleve/v2"
)

const DefaultRRFK = 60

// Diagnostics reports retrieval mechanisms that could not be used. Domains
// remain responsible for deciding how this state is exposed in their public
// contracts.
type Diagnostics struct {
	VectorDegraded   bool
	RerankerDegraded bool
}

func (d Diagnostics) Degraded() bool {
	return d.VectorDegraded || d.RerankerDegraded
}

// LexicalIndex is a small in-memory lexical index over caller-provided compact
// retrieval text. The caller owns the domain model and the text projection.
type LexicalIndex struct {
	index bleve.Index
}

func NewLexicalIndex() (*LexicalIndex, error) {
	index, err := bleve.NewMemOnly(bleve.NewIndexMapping())
	if err != nil {
		return nil, err
	}
	return &LexicalIndex{index: index}, nil
}

func (l *LexicalIndex) Index(id, text string) error {
	if l == nil || l.index == nil {
		return errors.New("lexical index is nil")
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("lexical document id is required")
	}
	return l.index.Index(id, text)
}

func (l *LexicalIndex) Delete(id string) error {
	if l == nil || l.index == nil {
		return errors.New("lexical index is nil")
	}
	return l.index.Delete(id)
}

// Search performs natural-language matching. MatchQuery intentionally avoids
// interpreting ordinary user text as Bleve query-string syntax.
func (l *LexicalIndex) Search(query string, depth int) ([]string, error) {
	if l == nil || l.index == nil {
		return nil, errors.New("lexical index is nil")
	}
	if depth < 1 {
		return nil, nil
	}
	req := bleve.NewSearchRequestOptions(bleve.NewMatchQuery(query), depth, 0, false)
	result, err := l.index.Search(req)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(result.Hits))
	for _, hit := range result.Hits {
		ids = append(ids, hit.ID)
	}
	return ids, nil
}

func (l *LexicalIndex) Close() error {
	if l == nil || l.index == nil {
		return nil
	}
	return l.index.Close()
}

// RankedID records an ID and its one-based rank in one retrieval source.
type RankedID struct {
	ID   string
	Rank int
}

// RankIDs converts ordered IDs to one-based ranks. Duplicate IDs use their
// first (best) occurrence so a malformed source cannot contribute twice.
func RankIDs(ids []string) []RankedID {
	seen := make(map[string]struct{}, len(ids))
	ranked := make([]RankedID, 0, len(ids))
	for index, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ranked = append(ranked, RankedID{ID: id, Rank: index + 1})
	}
	return ranked
}

// FusedResult is one reciprocal-rank-fusion result. Ranks corresponds to the
// source order passed to ReciprocalRankFusion; zero means absent from a source.
type FusedResult struct {
	ID    string
	Score float64
	Ranks []int
}

// ReciprocalRankFusion combines ranked sources and returns deterministic score
// descending, ID ascending output. Within one source, duplicate IDs contribute
// only their best rank.
func ReciprocalRankFusion(k int, sources ...[]RankedID) []FusedResult {
	if k < 1 {
		k = DefaultRRFK
	}
	type state struct {
		id    string
		ranks []int
	}
	states := make(map[string]*state)
	for sourceIndex, source := range sources {
		for _, item := range source {
			if item.Rank < 1 {
				continue
			}
			current := states[item.ID]
			if current == nil {
				current = &state{id: item.ID, ranks: make([]int, len(sources))}
				states[item.ID] = current
			}
			if current.ranks[sourceIndex] == 0 || item.Rank < current.ranks[sourceIndex] {
				current.ranks[sourceIndex] = item.Rank
			}
		}
	}
	results := make([]FusedResult, 0, len(states))
	for _, current := range states {
		score := 0.0
		for _, rank := range current.ranks {
			if rank > 0 {
				score += 1.0 / float64(k+rank)
			}
		}
		results = append(results, FusedResult{ID: current.id, Score: score, Ranks: append([]int(nil), current.ranks...)})
	}
	sort.Slice(results, func(a, b int) bool {
		if results[a].Score == results[b].Score {
			return results[a].ID < results[b].ID
		}
		return results[a].Score > results[b].Score
	})
	return results
}

// VectorCandidate is the minimal projection needed for dense similarity.
type VectorCandidate struct {
	ID     string
	Vector []float32
}

// ScoredID is a deterministic similarity result.
type ScoredID struct {
	ID    string
	Score float64
}

// RankByCosine ranks candidates by cosine similarity, then ID. Duplicate IDs
// keep the highest score so output is deterministic and contains unique IDs.
func RankByCosine(query []float32, candidates []VectorCandidate, depth int) []ScoredID {
	best := make(map[string]float64, len(candidates))
	for _, candidate := range candidates {
		score := Cosine(query, candidate.Vector)
		previous, exists := best[candidate.ID]
		if !exists || score > previous {
			best[candidate.ID] = score
		}
	}
	results := make([]ScoredID, 0, len(best))
	for id, score := range best {
		results = append(results, ScoredID{ID: id, Score: score})
	}
	sort.Slice(results, func(a, b int) bool {
		if results[a].Score == results[b].Score {
			return results[a].ID < results[b].ID
		}
		return results[a].Score > results[b].Score
	})
	if depth > 0 && len(results) > depth {
		results = results[:depth]
	}
	return results
}

func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, aa, bb float64
	for index := range a {
		x, y := float64(a[index]), float64(b[index])
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}

// RerankResult is a domain-neutral ordered reranker decision.
type RerankResult struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Reranker ranks a bounded domain-specific candidate projection for a query.
// Candidate types remain owned by the importing domain/adapter.
type Reranker[C any] interface {
	Rerank(context.Context, string, []C) ([]RerankResult, error)
}

var ErrInvalidRerank = errors.New("invalid rerank result")

// ApplyRerank applies an exact reranker permutation to domain-owned items.
// It rejects missing, unknown, or duplicate IDs rather than silently dropping
// candidates. withReason may be nil when the domain does not expose reasons.
func ApplyRerank[T any](items []T, ordered []RerankResult, id func(T) string, withReason func(T, string) T) ([]T, error) {
	if id == nil {
		return nil, fmt.Errorf("%w: id function is required", ErrInvalidRerank)
	}
	if len(items) != len(ordered) {
		return nil, fmt.Errorf("%w: ranking must contain every item exactly once", ErrInvalidRerank)
	}
	byID := make(map[string]T, len(items))
	for _, item := range items {
		itemID := id(item)
		if strings.TrimSpace(itemID) == "" {
			return nil, fmt.Errorf("%w: item ID is required", ErrInvalidRerank)
		}
		if _, exists := byID[itemID]; exists {
			return nil, fmt.Errorf("%w: item IDs must be unique", ErrInvalidRerank)
		}
		byID[itemID] = item
	}
	seen := make(map[string]struct{}, len(ordered))
	result := make([]T, 0, len(ordered))
	for _, decision := range ordered {
		if _, exists := seen[decision.ID]; exists {
			return nil, fmt.Errorf("%w: ranking contains duplicate ID %q", ErrInvalidRerank, decision.ID)
		}
		item, exists := byID[decision.ID]
		if !exists {
			return nil, fmt.Errorf("%w: ranking contains unknown ID %q", ErrInvalidRerank, decision.ID)
		}
		seen[decision.ID] = struct{}{}
		if withReason != nil {
			item = withReason(item, strings.TrimSpace(decision.Reason))
		}
		result = append(result, item)
	}
	return result, nil
}
