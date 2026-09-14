package knowledge

import (
	"context"
	"fmt"
	"strings"
)

// DocumentIDForChunk resolves a caller-selected chunk to its stable document
// identity without disclosing chunk content. Authorization adapters can use
// this before ReadOKF so resource grants remain stable across content revisions.
func (s *Service) DocumentIDForChunk(ctx context.Context, chunkID string) (string, error) {
	if s == nil || strings.TrimSpace(chunkID) == "" {
		return "", fmt.Errorf("chunk id is required")
	}
	if err := s.ensureOKFSchema(ctx); err != nil {
		return "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	chunk, ok := s.index.read(chunkID)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrChunkNotFound, chunkID)
	}
	return chunk.DocumentID, nil
}
