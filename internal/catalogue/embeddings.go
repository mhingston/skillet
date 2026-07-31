package catalogue

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// EmbeddingCache is the durable cache used by the search index. The cache key
// includes the provider identity and routing-document digest, so changing a
// model or routing representation cannot reuse stale vectors.
type EmbeddingCache struct{ DB *sql.DB }

func (s *EmbeddingCache) Load(provider, model string, dimensions int, digest string) ([]float32, bool, error) {
	if s == nil || s.DB == nil {
		return nil, false, fmt.Errorf("embedding cache database is required")
	}
	var raw []byte
	err := s.DB.QueryRow(`SELECT vector FROM embedding_cache WHERE provider=? AND model=? AND dimensions=? AND routing_document_digest=?`, provider, model, dimensions, digest).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(raw)%4 != 0 {
		return nil, false, fmt.Errorf("cached embedding has invalid byte length")
	}
	vector := make([]float32, len(raw)/4)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return vector, true, nil
}

func (s *EmbeddingCache) Save(provider, model string, dimensions int, digest string, vector []float32) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("embedding cache database is required")
	}
	raw := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(value))
	}
	_, err := s.DB.Exec(`INSERT INTO embedding_cache(provider, model, dimensions, routing_document_digest, vector, created_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(provider, model, dimensions, routing_document_digest) DO UPDATE SET vector=excluded.vector, created_at=excluded.created_at`, provider, model, dimensions, digest, raw, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
