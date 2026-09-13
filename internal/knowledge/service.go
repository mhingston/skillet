package knowledge

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrChunkNotFound = errors.New("knowledge chunk not found")

// Stats summarizes one published knowledge snapshot.
type Stats struct {
	Documents      int  `json:"documents"`
	Chunks         int  `json:"chunks"`
	VectorDegraded bool `json:"vector_degraded"`
}

// Service owns the authoritative SQLite catalogue and the derived retrieval
// index. Reindex builds the entire next state before atomically publishing it.
type Service struct {
	mu      sync.RWMutex
	catalog *catalogue
	index   *index
	options Options
}

// Open opens (or creates) a knowledge catalogue below dataDir and reconstructs
// the derived lexical index from its last committed snapshot.
func Open(ctx context.Context, dataDir string, options Options) (*Service, error) {
	catalog, err := openCatalogue(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	_, chunks, err := catalog.load(ctx)
	if err != nil {
		catalog.close()
		return nil, err
	}
	degraded := false
	if options.Embedder != nil {
		for _, chunk := range chunks {
			if len(chunk.Vector) == 0 {
				degraded = true
				break
			}
		}
	}
	idx, err := newIndex(chunks, options, degraded)
	if err != nil {
		catalog.close()
		return nil, fmt.Errorf("rebuild knowledge index: %w", err)
	}
	return &Service{catalog: catalog, index: idx, options: options}, nil
}

// Reindex discovers a complete source snapshot, embeds chunks where possible,
// builds the next Bleve index, and only then replaces the SQLite catalogue in a
// transaction. Malformed/unreadable input or index construction errors leave
// the previous authoritative snapshot untouched. Embedding failures are the
// documented exception: they publish a lexical-only/degraded snapshot.
func (s *Service) Reindex(ctx context.Context, sources []Source) (Stats, error) {
	documents, chunks, err := Discover(sources)
	if err != nil {
		return Stats{}, err
	}
	degraded := embedChunks(chunks, s.options.Embedder)
	next, err := newIndex(chunks, s.options, degraded)
	if err != nil {
		return Stats{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.catalog.replace(ctx, documents, chunks); err != nil {
		next.close()
		return Stats{}, err
	}
	previous := s.index
	s.index = next
	if previous != nil {
		_ = previous.close()
	}
	return Stats{Documents: len(documents), Chunks: len(chunks), VectorDegraded: degraded}, nil
}

// Search retrieves bounded snippets with source and ranking provenance.
func (s *Service) Search(ctx context.Context, query string, limit int) (SearchResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return SearchResponse{}, errors.New("knowledge index is unavailable")
	}
	return s.index.search(ctx, query, limit)
}

// Read returns the complete selected chunk after discovery. This is the second
// progressive-disclosure step; search results intentionally contain snippets.
func (s *Service) Read(chunkID string) (Chunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return Chunk{}, errors.New("knowledge index is unavailable")
	}
	chunk, exists := s.index.read(chunkID)
	if !exists {
		return Chunk{}, fmt.Errorf("%w: %s", ErrChunkNotFound, chunkID)
	}
	return chunk, nil
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var indexErr error
	if s.index != nil {
		indexErr = s.index.close()
		s.index = nil
	}
	catalogErr := s.catalog.close()
	if indexErr != nil {
		return indexErr
	}
	return catalogErr
}

func embedChunks(chunks []Chunk, embedder Embedder) bool {
	if embedder == nil {
		return len(chunks) > 0
	}
	degraded := false
	dimensions := 0
	for n := range chunks {
		vector, err := embedder.Embed(retrievalText(chunks[n]))
		if err != nil || len(vector) == 0 {
			degraded = true
			chunks[n].Vector = nil
			continue
		}
		if dimensions == 0 {
			dimensions = len(vector)
		}
		if len(vector) != dimensions {
			degraded = true
			chunks[n].Vector = nil
			continue
		}
		chunks[n].Vector = append([]float32(nil), vector...)
	}
	return degraded
}
