package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const knowledgeSchema = `
CREATE TABLE IF NOT EXISTS knowledge_documents (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    source_locator TEXT NOT NULL,
    source_revision TEXT NOT NULL DEFAULT '',
    relative_path TEXT NOT NULL,
    content_digest TEXT NOT NULL,
    UNIQUE(source_id, relative_path)
);
CREATE TABLE IF NOT EXISTS knowledge_chunks (
    id TEXT PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES knowledge_documents(id) ON DELETE CASCADE,
    heading TEXT NOT NULL DEFAULT '',
    heading_path_json TEXT NOT NULL DEFAULT '[]',
    content TEXT NOT NULL,
    content_digest TEXT NOT NULL,
    vector_json TEXT NOT NULL DEFAULT '[]'
);
`

type catalogue struct {
	db *sql.DB
}

func openCatalogue(ctx context.Context, dataDir string) (*catalogue, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("knowledge data directory is required")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create knowledge data directory: %w", err)
	}
	path := filepath.Join(dataDir, "knowledge.db")
	dsn := path + "?_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open knowledge sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure knowledge sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, knowledgeSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize knowledge schema: %w", err)
	}
	return &catalogue{db: db}, nil
}

// replace publishes one complete authoritative snapshot in a single SQLite
// transaction. A caller failure before commit leaves the previous catalogue
// authoritative; there is no partially visible reindex state.
func (c *catalogue) replace(ctx context.Context, documents []Document, chunks []Chunk) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin knowledge catalogue replacement: %w", err)
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_chunks"); err != nil {
		return rollback(fmt.Errorf("clear knowledge chunks: %w", err))
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_documents"); err != nil {
		return rollback(fmt.Errorf("clear knowledge documents: %w", err))
	}
	for _, document := range documents {
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_documents(id, source_id, source_locator, source_revision, relative_path, content_digest) VALUES(?,?,?,?,?,?)`,
			document.ID, document.SourceID, document.SourceLocator, document.SourceRevision, document.Path, document.ContentDigest); err != nil {
			return rollback(fmt.Errorf("insert knowledge document %q: %w", document.ID, err))
		}
	}
	for _, chunk := range chunks {
		headingPath, err := json.Marshal(chunk.HeadingPath)
		if err != nil {
			return rollback(fmt.Errorf("encode knowledge chunk heading path: %w", err))
		}
		vector, err := json.Marshal(chunk.Vector)
		if err != nil {
			return rollback(fmt.Errorf("encode knowledge chunk vector: %w", err))
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_chunks(id, document_id, heading, heading_path_json, content, content_digest, vector_json) VALUES(?,?,?,?,?,?,?)`,
			chunk.ID, chunk.DocumentID, chunk.Heading, string(headingPath), chunk.Content, chunk.ContentDigest, string(vector)); err != nil {
			return rollback(fmt.Errorf("insert knowledge chunk %q: %w", chunk.ID, err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge catalogue replacement: %w", err)
	}
	return nil
}

func (c *catalogue) load(ctx context.Context) ([]Document, []Chunk, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT id, source_id, source_locator, source_revision, relative_path, content_digest FROM knowledge_documents ORDER BY id`)
	if err != nil {
		return nil, nil, fmt.Errorf("load knowledge documents: %w", err)
	}
	documents := []Document{}
	byID := map[string]Document{}
	for rows.Next() {
		var document Document
		if err := rows.Scan(&document.ID, &document.SourceID, &document.SourceLocator, &document.SourceRevision, &document.Path, &document.ContentDigest); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("scan knowledge document: %w", err)
		}
		documents = append(documents, document)
		byID[document.ID] = document
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("iterate knowledge documents: %w", err)
	}
	rows.Close()

	chunkRows, err := c.db.QueryContext(ctx, `SELECT id, document_id, heading, heading_path_json, content, content_digest, vector_json FROM knowledge_chunks ORDER BY id`)
	if err != nil {
		return nil, nil, fmt.Errorf("load knowledge chunks: %w", err)
	}
	defer chunkRows.Close()
	chunks := []Chunk{}
	for chunkRows.Next() {
		var chunk Chunk
		var headingPathJSON, vectorJSON string
		if err := chunkRows.Scan(&chunk.ID, &chunk.DocumentID, &chunk.Heading, &headingPathJSON, &chunk.Content, &chunk.ContentDigest, &vectorJSON); err != nil {
			return nil, nil, fmt.Errorf("scan knowledge chunk: %w", err)
		}
		document, exists := byID[chunk.DocumentID]
		if !exists {
			return nil, nil, fmt.Errorf("knowledge chunk %q references unknown document %q", chunk.ID, chunk.DocumentID)
		}
		if err := json.Unmarshal([]byte(headingPathJSON), &chunk.HeadingPath); err != nil {
			return nil, nil, fmt.Errorf("decode knowledge chunk %q heading path: %w", chunk.ID, err)
		}
		if err := json.Unmarshal([]byte(vectorJSON), &chunk.Vector); err != nil {
			return nil, nil, fmt.Errorf("decode knowledge chunk %q vector: %w", chunk.ID, err)
		}
		chunk.SourceID = document.SourceID
		chunk.SourceLocator = document.SourceLocator
		chunk.SourceRevision = document.SourceRevision
		chunk.Path = document.Path
		chunk.DocumentDigest = document.ContentDigest
		chunks = append(chunks, chunk)
	}
	if err := chunkRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate knowledge chunks: %w", err)
	}
	return documents, chunks, nil
}

func (c *catalogue) close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}
