package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
)

const okfSchema = `
CREATE TABLE IF NOT EXISTS knowledge_okf_documents (
    document_id TEXT PRIMARY KEY REFERENCES knowledge_documents(id) ON DELETE CASCADE,
    metadata_json TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS knowledge_okf_links (
    source_document_id TEXT NOT NULL REFERENCES knowledge_documents(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    link_text TEXT NOT NULL DEFAULT '',
    target_path TEXT NOT NULL,
    target_document_id TEXT,
    resolved INTEGER NOT NULL,
    PRIMARY KEY(source_document_id, ordinal)
);
CREATE INDEX IF NOT EXISTS knowledge_okf_links_target_idx ON knowledge_okf_links(target_document_id);
`

// OKFSearchResult adds OKF provenance to the existing bounded retrieval result.
type OKFSearchResult struct {
	Result   Result   `json:"result"`
	Metadata Metadata `json:"metadata"`
}

type OKFSearchResponse struct {
	Results     []OKFSearchResult `json:"results"`
	Diagnostics any               `json:"diagnostics"`
}

// OKFRead is the progressive-disclosure read projection. Full document bodies
// are still not enumerated by search; callers select one returned chunk first.
type OKFRead struct {
	Chunk    Chunk    `json:"chunk"`
	Metadata Metadata `json:"metadata"`
	Links    []Link   `json:"links,omitempty"`
}

// ReindexOKF validates a complete bundle snapshot, builds the derived index,
// then atomically replaces documents, chunks, OKF metadata, and explicit links.
// Validation/build failures leave the last authoritative state unchanged.
func (s *Service) ReindexOKF(ctx context.Context, bundles []OKFBundle) (Stats, error) {
	snapshot, err := DiscoverOKF(bundles)
	if err != nil {
		return Stats{}, err
	}
	if err := s.ensureOKFSchema(ctx); err != nil {
		return Stats{}, err
	}
	degraded := embedChunks(snapshot.Chunks, s.options.Embedder)
	next, err := newIndex(snapshot.Chunks, s.options, degraded)
	if err != nil {
		return Stats{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.catalog.db.BeginTx(ctx, nil)
	if err != nil {
		next.close()
		return Stats{}, fmt.Errorf("begin OKF catalogue replacement: %w", err)
	}
	rollback := func(cause error) (Stats, error) {
		_ = tx.Rollback()
		_ = next.close()
		return Stats{}, cause
	}
	for _, statement := range []string{
		"DELETE FROM knowledge_okf_links",
		"DELETE FROM knowledge_okf_documents",
		"DELETE FROM knowledge_chunks",
		"DELETE FROM knowledge_documents",
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return rollback(fmt.Errorf("clear OKF catalogue: %w", err))
		}
	}
	for _, document := range snapshot.Documents {
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_documents(id, source_id, source_locator, source_revision, relative_path, content_digest) VALUES(?,?,?,?,?,?)`, document.ID, document.SourceID, document.SourceLocator, document.SourceRevision, document.Path, document.ContentDigest); err != nil {
			return rollback(fmt.Errorf("insert OKF document %q: %w", document.ID, err))
		}
		detail := snapshot.Details[document.ID]
		metadata, err := json.Marshal(detail.Metadata)
		if err != nil {
			return rollback(fmt.Errorf("encode OKF metadata %q: %w", document.ID, err))
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_okf_documents(document_id, metadata_json) VALUES(?,?)`, document.ID, string(metadata)); err != nil {
			return rollback(fmt.Errorf("insert OKF metadata %q: %w", document.ID, err))
		}
		for ordinal, link := range detail.Links {
			var target any
			if link.TargetDocumentID != "" {
				target = link.TargetDocumentID
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_okf_links(source_document_id, ordinal, link_text, target_path, target_document_id, resolved) VALUES(?,?,?,?,?,?)`, document.ID, ordinal, link.Text, link.TargetPath, target, link.Resolved); err != nil {
				return rollback(fmt.Errorf("insert OKF link %q/%d: %w", document.ID, ordinal, err))
			}
		}
	}
	for _, chunk := range snapshot.Chunks {
		headingPath, err := json.Marshal(chunk.HeadingPath)
		if err != nil {
			return rollback(fmt.Errorf("encode OKF chunk heading path: %w", err))
		}
		vector, err := json.Marshal(chunk.Vector)
		if err != nil {
			return rollback(fmt.Errorf("encode OKF chunk vector: %w", err))
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_chunks(id, document_id, heading, heading_path_json, content, content_digest, vector_json) VALUES(?,?,?,?,?,?,?)`, chunk.ID, chunk.DocumentID, chunk.Heading, string(headingPath), chunk.Content, chunk.ContentDigest, string(vector)); err != nil {
			return rollback(fmt.Errorf("insert OKF chunk %q: %w", chunk.ID, err))
		}
	}
	if err := tx.Commit(); err != nil {
		_ = next.close()
		return Stats{}, fmt.Errorf("commit OKF catalogue replacement: %w", err)
	}
	previous := s.index
	s.index = next
	if previous != nil {
		_ = previous.close()
	}
	return Stats{Documents: len(snapshot.Documents), Chunks: len(snapshot.Chunks), VectorDegraded: degraded}, nil
}

// SearchOKF performs the existing knowledge search and enriches each compact
// result with preserved OKF metadata/provenance.
func (s *Service) SearchOKF(ctx context.Context, query string, limit int) (OKFSearchResponse, error) {
	if err := s.ensureOKFSchema(ctx); err != nil {
		return OKFSearchResponse{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	response, err := s.index.search(ctx, query, limit)
	if err != nil {
		return OKFSearchResponse{}, err
	}
	results := make([]OKFSearchResult, 0, len(response.Results))
	for _, result := range response.Results {
		metadata, _, err := s.loadOKFDetail(ctx, result.DocumentID)
		if err != nil {
			return OKFSearchResponse{}, err
		}
		results = append(results, OKFSearchResult{Result: result, Metadata: metadata})
	}
	return OKFSearchResponse{Results: results, Diagnostics: response.Diagnostics}, nil
}

// ReadOKF returns one selected chunk plus its document-level OKF provenance and
// explicit outgoing links.
func (s *Service) ReadOKF(ctx context.Context, chunkID string) (OKFRead, error) {
	if err := s.ensureOKFSchema(ctx); err != nil {
		return OKFRead{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	chunk, ok := s.index.read(chunkID)
	if !ok {
		return OKFRead{}, fmt.Errorf("%w: %s", ErrChunkNotFound, chunkID)
	}
	metadata, links, err := s.loadOKFDetail(ctx, chunk.DocumentID)
	if err != nil {
		return OKFRead{}, err
	}
	return OKFRead{Chunk: chunk, Metadata: metadata, Links: links}, nil
}

// GetBacklinks returns only resolved, explicit Markdown links into documentID.
func (s *Service) GetBacklinks(ctx context.Context, documentID string) ([]Backlink, error) {
	if err := s.ensureOKFSchema(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.catalog.db.QueryContext(ctx, `
SELECT l.source_document_id, d.relative_path, m.metadata_json, l.link_text
FROM knowledge_okf_links l
JOIN knowledge_documents d ON d.id = l.source_document_id
JOIN knowledge_okf_documents m ON m.document_id = l.source_document_id
WHERE l.resolved = 1 AND l.target_document_id = ?
ORDER BY d.relative_path, l.ordinal`, documentID)
	if err != nil {
		return nil, fmt.Errorf("query OKF backlinks: %w", err)
	}
	defer rows.Close()
	var backlinks []Backlink
	for rows.Next() {
		var backlink Backlink
		var metadataJSON string
		if err := rows.Scan(&backlink.DocumentID, &backlink.Path, &metadataJSON, &backlink.LinkText); err != nil {
			return nil, err
		}
		var metadata Metadata
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			return nil, fmt.Errorf("decode OKF backlink metadata: %w", err)
		}
		backlink.Title = metadata.Title
		backlinks = append(backlinks, backlink)
	}
	return backlinks, rows.Err()
}

func (s *Service) ensureOKFSchema(ctx context.Context) error {
	_, err := s.catalog.db.ExecContext(ctx, okfSchema)
	if err != nil {
		return fmt.Errorf("initialize OKF schema: %w", err)
	}
	return nil
}

func (s *Service) loadOKFDetail(ctx context.Context, documentID string) (Metadata, []Link, error) {
	var metadataJSON string
	if err := s.catalog.db.QueryRowContext(ctx, `SELECT metadata_json FROM knowledge_okf_documents WHERE document_id = ?`, documentID).Scan(&metadataJSON); err != nil {
		return Metadata{}, nil, fmt.Errorf("load OKF metadata for %q: %w", documentID, err)
	}
	var metadata Metadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return Metadata{}, nil, fmt.Errorf("decode OKF metadata for %q: %w", documentID, err)
	}
	rows, err := s.catalog.db.QueryContext(ctx, `SELECT link_text, target_path, COALESCE(target_document_id, ''), resolved FROM knowledge_okf_links WHERE source_document_id = ? ORDER BY ordinal`, documentID)
	if err != nil {
		return Metadata{}, nil, err
	}
	defer rows.Close()
	var links []Link
	for rows.Next() {
		var link Link
		if err := rows.Scan(&link.Text, &link.TargetPath, &link.TargetDocumentID, &link.Resolved); err != nil {
			return Metadata{}, nil, err
		}
		links = append(links, link)
	}
	return metadata, links, rows.Err()
}
