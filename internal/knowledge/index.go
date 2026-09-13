package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mhingston/skillet/internal/retrieval"
)

const (
	defaultLexicalDepth = 50
	defaultVectorDepth  = 50
	defaultResultLimit  = 5
	maxResultLimit      = 50
	defaultRerankLimit  = 20
)

// Embedder is the existing provider-neutral embedding contract used by Skillet
// retrieval. internal/embedding.Client satisfies this interface directly.
type Embedder interface {
	Embed(string) ([]float32, error)
}

// Candidate is the bounded knowledge projection offered to an optional
// reranker. It intentionally contains a snippet rather than full document text.
type Candidate struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id"`
	Path       string `json:"path"`
	Heading    string `json:"heading,omitempty"`
	Snippet    string `json:"snippet"`
}

// Options configures domain-specific knowledge retrieval while sharing only
// retrieval mechanics with capability search.
type Options struct {
	Embedder     Embedder
	Reranker     retrieval.Reranker[Candidate]
	LexicalDepth int
	VectorDepth  int
	RRFK         int
	RerankLimit  int
}

// Result carries progressive-disclosure content plus immutable source and rank
// evidence sufficient for deterministic evaluation.
type Result struct {
	Rank           int      `json:"rank"`
	DocumentID     string   `json:"document_id"`
	ChunkID        string   `json:"chunk_id"`
	SourceID       string   `json:"source_id"`
	SourceLocator  string   `json:"source_locator"`
	SourceRevision string   `json:"source_revision"`
	Path           string   `json:"path"`
	DocumentDigest string   `json:"document_digest"`
	Heading        string   `json:"heading,omitempty"`
	HeadingPath    []string `json:"heading_path,omitempty"`
	Snippet        string   `json:"snippet"`
	ContentDigest  string   `json:"content_digest"`
	LexicalRank    int      `json:"lexical_rank,omitempty"`
	VectorRank     int      `json:"vector_rank,omitempty"`
	FusedScore     float64  `json:"fused_score"`
	RerankReason   string   `json:"rerank_reason,omitempty"`
}

// SearchResponse makes provider degradation explicit without failing lexical
// retrieval that remains valid.
type SearchResponse struct {
	Results     []Result              `json:"results"`
	Diagnostics retrieval.Diagnostics `json:"diagnostics"`
}

type index struct {
	lexical           *retrieval.LexicalIndex
	chunks            map[string]Chunk
	embedder          Embedder
	reranker          retrieval.Reranker[Candidate]
	lexicalDepth      int
	vectorDepth       int
	rrfK              int
	rerankLimit       int
	embeddingDegraded bool
}

func newIndex(chunks []Chunk, options Options, embeddingDegraded bool) (*index, error) {
	lexical, err := retrieval.NewLexicalIndex()
	if err != nil {
		return nil, err
	}
	values := make(map[string]Chunk, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.ID) == "" {
			lexical.Close()
			return nil, errors.New("knowledge chunk id is required")
		}
		if _, exists := values[chunk.ID]; exists {
			lexical.Close()
			return nil, fmt.Errorf("duplicate knowledge chunk id %q", chunk.ID)
		}
		values[chunk.ID] = chunk
		if err := lexical.Index(chunk.ID, retrievalText(chunk)); err != nil {
			lexical.Close()
			return nil, fmt.Errorf("index knowledge chunk %q: %w", chunk.ID, err)
		}
	}
	lexicalDepth := options.LexicalDepth
	if lexicalDepth < 1 {
		lexicalDepth = defaultLexicalDepth
	}
	vectorDepth := options.VectorDepth
	if vectorDepth < 1 {
		vectorDepth = defaultVectorDepth
	}
	rrfK := options.RRFK
	if rrfK < 1 {
		rrfK = retrieval.DefaultRRFK
	}
	rerankLimit := options.RerankLimit
	if rerankLimit < 1 {
		rerankLimit = defaultRerankLimit
	}
	return &index{
		lexical: lexical, chunks: values, embedder: options.Embedder, reranker: options.Reranker,
		lexicalDepth: lexicalDepth, vectorDepth: vectorDepth, rrfK: rrfK,
		rerankLimit: rerankLimit, embeddingDegraded: embeddingDegraded,
	}, nil
}

func (i *index) search(ctx context.Context, query string, limit int) (SearchResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return SearchResponse{}, errors.New("knowledge query is required")
	}
	if limit < 1 {
		limit = defaultResultLimit
	}
	if limit > maxResultLimit {
		limit = maxResultLimit
	}

	lexicalIDs, err := i.lexical.Search(query, i.lexicalDepth)
	if err != nil {
		return SearchResponse{}, fmt.Errorf("lexical knowledge search: %w", err)
	}
	lexicalRanks := retrieval.RankIDs(lexicalIDs)
	diagnostics := retrieval.Diagnostics{VectorDegraded: i.embeddingDegraded}

	var vectorRanks []retrieval.RankedID
	if i.embedder == nil {
		diagnostics.VectorDegraded = true
	} else {
		queryVector, embedErr := i.embedder.Embed(query)
		if embedErr != nil || len(queryVector) == 0 {
			diagnostics.VectorDegraded = true
		} else {
			candidates := make([]retrieval.VectorCandidate, 0, len(i.chunks))
			for id, chunk := range i.chunks {
				if len(chunk.Vector) == 0 {
					continue
				}
				candidates = append(candidates, retrieval.VectorCandidate{ID: id, Vector: chunk.Vector})
			}
			ranked := retrieval.RankByCosine(queryVector, candidates, i.vectorDepth)
			ids := make([]string, 0, len(ranked))
			for _, candidate := range ranked {
				// Zero or negative cosine values are evidence of no semantic
				// match, not candidates merely because a vector exists.
				if candidate.Score <= 0 {
					continue
				}
				ids = append(ids, candidate.ID)
			}
			vectorRanks = retrieval.RankIDs(ids)
		}
	}

	fused := retrieval.ReciprocalRankFusion(i.rrfK, lexicalRanks, vectorRanks)
	if len(fused) > limit {
		fused = fused[:limit]
	}
	results := make([]Result, 0, len(fused))
	for _, item := range fused {
		chunk, exists := i.chunks[item.ID]
		if !exists {
			continue
		}
		lexicalRank, vectorRank := 0, 0
		if len(item.Ranks) > 0 {
			lexicalRank = item.Ranks[0]
		}
		if len(item.Ranks) > 1 {
			vectorRank = item.Ranks[1]
		}
		results = append(results, Result{
			DocumentID: chunk.DocumentID, ChunkID: chunk.ID, SourceID: chunk.SourceID,
			SourceLocator: chunk.SourceLocator, SourceRevision: chunk.SourceRevision, Path: chunk.Path,
			DocumentDigest: chunk.DocumentDigest, Heading: chunk.Heading,
			HeadingPath: append([]string(nil), chunk.HeadingPath...), Snippet: snippet(chunk.Content),
			ContentDigest: chunk.ContentDigest, LexicalRank: lexicalRank, VectorRank: vectorRank,
			FusedScore: item.Score,
		})
	}

	if i.reranker != nil && len(results) > 1 {
		count := len(results)
		if count > i.rerankLimit {
			count = i.rerankLimit
		}
		candidates := make([]Candidate, count)
		for n := 0; n < count; n++ {
			candidates[n] = Candidate{
				ID: results[n].ChunkID, DocumentID: results[n].DocumentID,
				Path: results[n].Path, Heading: results[n].Heading, Snippet: results[n].Snippet,
			}
		}
		decisions, rerankErr := i.reranker.Rerank(ctx, query, candidates)
		if rerankErr != nil {
			diagnostics.RerankerDegraded = true
		} else {
			reranked, applyErr := retrieval.ApplyRerank(results[:count], decisions,
				func(result Result) string { return result.ChunkID },
				func(result Result, reason string) Result { result.RerankReason = reason; return result },
			)
			if applyErr != nil {
				diagnostics.RerankerDegraded = true
			} else {
				results = append(reranked, results[count:]...)
			}
		}
	}
	for n := range results {
		results[n].Rank = n + 1
	}
	return SearchResponse{Results: results, Diagnostics: diagnostics}, nil
}

func (i *index) read(chunkID string) (Chunk, bool) {
	chunk, exists := i.chunks[chunkID]
	if !exists {
		return Chunk{}, false
	}
	chunk.HeadingPath = append([]string(nil), chunk.HeadingPath...)
	chunk.Vector = nil
	return chunk, true
}

func (i *index) close() error {
	if i == nil || i.lexical == nil {
		return nil
	}
	return i.lexical.Close()
}

func snippet(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	runes := []rune(content)
	const maxRunes = 280
	if len(runes) <= maxRunes {
		return content
	}
	return string(runes[:maxRunes]) + "…"
}
