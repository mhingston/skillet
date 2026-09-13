package search

import (
	"fmt"
	"strings"

	"github.com/mhingston/skillet/internal/retrieval"
)

// SearchEligible is the filter-before-ranking variant used for policy
// eligibility boundaries such as scoped capability discovery. It preserves the
// existing SearchWithFilters behaviour for v1 callers while compacting source
// ranks after filtering so ineligible documents cannot affect RRF scores.
//
// Callers should choose lexical/vector depths large enough to cover the
// unfiltered candidate pool when an eligibility boundary must be exhaustive.
func (i *Index) SearchEligible(query string, lexicalDepth, vectorDepth, limit, rrfK int, filters Filters) ([]Hit, bool, error) {
	if strings.TrimSpace(query) == "" {
		return nil, false, fmt.Errorf("query is required")
	}
	if limit < 1 {
		return nil, false, fmt.Errorf("limit must be positive")
	}
	if lexicalDepth < 1 {
		lexicalDepth = 50
	}
	if vectorDepth < 1 {
		vectorDepth = 50
	}
	if rrfK < 1 {
		rrfK = retrieval.DefaultRRFK
	}

	i.mu.RLock()
	defer i.mu.RUnlock()
	lexicalHits, err := i.lexicalSearch(query, lexicalDepth)
	if err != nil {
		return nil, false, err
	}
	vectorHits, degraded := i.vectorSearch(query, vectorDepth)
	lexicalRanks := compactFilterRanks(retrieval.RankIDs(lexicalHits), i.docs, filters)
	vectorRanks := compactFilterRanks(retrieval.RankIDs(vectorHits), i.docs, filters)
	fused := retrieval.ReciprocalRankFusion(rrfK, lexicalRanks, vectorRanks)
	result := make([]Hit, 0, len(fused))
	for _, item := range fused {
		lexicalRank, vectorRank := 0, 0
		if len(item.Ranks) > 0 {
			lexicalRank = item.Ranks[0]
		}
		if len(item.Ranks) > 1 {
			vectorRank = item.Ranks[1]
		}
		result = append(result, Hit{
			ID:            item.ID,
			Score:         item.Score,
			LexicalRank:   lexicalRank,
			VectorRank:    vectorRank,
			MatchedFields: matchedFields(i.docs[item.ID], query),
		})
	}
	if len(result) > limit {
		result = result[:limit]
	}
	for n := range result {
		result[n].Rank = n + 1
		result[n].SemanticNeighbors = i.semanticNeighborsLocked(i.docs[result[n].ID], filters, semanticNeighborLimit)
	}
	return result, degraded, nil
}

func compactFilterRanks(ranks []retrieval.RankedID, docs map[string]Document, filters Filters) []retrieval.RankedID {
	filtered := make([]retrieval.RankedID, 0, len(ranks))
	for _, ranked := range ranks {
		if matches(docs[ranked.ID], filters) {
			filtered = append(filtered, retrieval.RankedID{ID: ranked.ID, Rank: len(filtered) + 1})
		}
	}
	return filtered
}
