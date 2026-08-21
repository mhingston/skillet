// Package search implements first-stage routing retrieval. It indexes only
// compact routing documents, never complete SKILL.md bodies.
package search

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/blevesearch/bleve/v2"
)

type Document struct {
	ID             string            `json:"id"`
	SkillID        string            `json:"skill_id,omitempty"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Version        string            `json:"version,omitempty"`
	Compatibility  string            `json:"compatibility,omitempty"`
	OrganizationID string            `json:"organization_id,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	RepositoryID   string            `json:"repository_id,omitempty"`
	Path           string            `json:"path,omitempty"`
	Commit         string            `json:"commit,omitempty"`
	Tree           string            `json:"tree,omitempty"`
	TrustLevel     string            `json:"trust_level,omitempty"`
	HasScripts     bool              `json:"has_scripts"`
	Vector         []float32         `json:"-"`
	Searchable     bool              `json:"-"`
}
type Filters struct {
	OrganizationID string
	Repositories   []string
	TrustLevels    []string
	HasScripts     *bool
	Metadata       map[string]string
}
type Embedder interface {
	Embed(string) ([]float32, error)
}
type Hit struct {
	ID                      string   `json:"-"`
	Score                   float64  `json:"-"`
	LexicalRank, VectorRank int      `json:"-"`
	Rank                    int      `json:"rank"`
	MatchedFields           []string `json:"matched_fields"`
	Reason                  string   `json:"reason,omitempty"`
}
type Index struct {
	mu                sync.RWMutex
	lexical           bleve.Index
	docs              map[string]Document
	embedder          Embedder
	embeddingDegraded bool
	embeddingObserver func()
}

func New(embedder Embedder) (*Index, error) {
	lexical, err := bleve.NewMemOnly(bleve.NewIndexMapping())
	if err != nil {
		return nil, err
	}
	return &Index{lexical: lexical, docs: map[string]Document{}, embedder: embedder}, nil
}

// SetEmbeddingObserver installs a callback invoked for every embedding
// provider request, including indexing and query retrieval. The callback must
// be non-blocking; it is intended for counters and tracing hooks.
func (i *Index) SetEmbeddingObserver(observer func()) {
	i.mu.Lock()
	i.embeddingObserver = observer
	i.mu.Unlock()
}
func (i *Index) observeEmbedding() {
	i.mu.RLock()
	observer := i.embeddingObserver
	i.mu.RUnlock()
	if observer != nil {
		observer()
	}
}
func (i *Index) Add(doc Document) error {
	if doc.ID == "" {
		return fmt.Errorf("document id is required")
	}
	embeddingFailed := false
	if i.embedder != nil && len(doc.Vector) == 0 {
		i.observeEmbedding()
		if vector, err := i.embedder.Embed(routingText(doc)); err == nil {
			doc.Vector = vector
		} else {
			embeddingFailed = true
		}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if embeddingFailed {
		i.embeddingDegraded = true
	}
	i.docs[doc.ID] = doc
	if !doc.Searchable {
		return i.lexical.Delete(doc.ID)
	}
	return i.lexical.Index(doc.ID, routingText(doc))
}
func (i *Index) Remove(id string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.docs, id)
	return i.lexical.Delete(id)
}

// Rebuild replaces the derived lexical index and document set atomically from
// the authoritative catalogue snapshot.
func (i *Index) Rebuild(docs []Document) error {
	lexical, err := bleve.NewMemOnly(bleve.NewIndexMapping())
	if err != nil {
		return err
	}
	values := make(map[string]Document, len(docs))
	embeddingDegraded := false
	for _, doc := range docs {
		if doc.ID == "" {
			_ = lexical.Close()
			return fmt.Errorf("document id is required")
		}
		values[doc.ID] = doc
		if i.embedder != nil && len(doc.Vector) == 0 {
			i.observeEmbedding()
			if vector, err := i.embedder.Embed(routingText(doc)); err == nil {
				doc.Vector = vector
				values[doc.ID] = doc
			} else {
				embeddingDegraded = true
			}
		}
		if doc.Searchable {
			if err := lexical.Index(doc.ID, routingText(doc)); err != nil {
				_ = lexical.Close()
				return err
			}
		}
	}
	i.mu.Lock()
	old := i.lexical
	i.lexical, i.docs, i.embeddingDegraded = lexical, values, embeddingDegraded
	i.mu.Unlock()
	return old.Close()
}
func (i *Index) Document(id string) (Document, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	doc, ok := i.docs[id]
	return doc, ok
}

// List returns the active, searchable routing documents in deterministic order.
// It is intended for catalogue browsing; it does not create search candidates.
func (i *Index) List(filters Filters) []Document {
	i.mu.RLock()
	defer i.mu.RUnlock()
	docs := make([]Document, 0, len(i.docs))
	for _, doc := range i.docs {
		if doc.Searchable && matches(doc, filters) {
			docs = append(docs, doc)
		}
	}
	sort.Slice(docs, func(a, b int) bool {
		if docs[a].Name == docs[b].Name {
			return docs[a].ID < docs[b].ID
		}
		return docs[a].Name < docs[b].Name
	})
	return docs
}

func (i *Index) Search(query string, lexicalDepth, vectorDepth, limit, rrfK int) ([]Hit, bool, error) {
	return i.SearchWithFilters(query, lexicalDepth, vectorDepth, limit, rrfK, Filters{})
}

func (i *Index) SearchWithFilters(query string, lexicalDepth, vectorDepth, limit, rrfK int, filters Filters) ([]Hit, bool, error) {
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
		rrfK = 60
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	lexicalHits, err := i.lexicalSearch(query, lexicalDepth)
	if err != nil {
		return nil, false, err
	}
	vectorHits, degraded := i.vectorSearch(query, vectorDepth)
	type rank struct {
		id              string
		lexical, vector int
	}
	ranks := map[string]*rank{}
	for n, id := range lexicalHits {
		if !matches(i.docs[id], filters) {
			continue
		}
		ranks[id] = &rank{id: id, lexical: n + 1}
	}
	for n, id := range vectorHits {
		if !matches(i.docs[id], filters) {
			continue
		}
		if r := ranks[id]; r != nil {
			r.vector = n + 1
		} else {
			ranks[id] = &rank{id: id, vector: n + 1}
		}
	}
	result := make([]Hit, 0, len(ranks))
	for _, r := range ranks {
		score := 0.0
		if r.lexical > 0 {
			score += 1.0 / float64(rrfK+r.lexical)
		}
		if r.vector > 0 {
			score += 1.0 / float64(rrfK+r.vector)
		}
		h := Hit{ID: r.id, Score: score, LexicalRank: r.lexical, VectorRank: r.vector, MatchedFields: matchedFields(i.docs[r.id], query)}
		result = append(result, h)
	}
	sort.Slice(result, func(a, b int) bool {
		if result[a].Score == result[b].Score {
			return result[a].ID < result[b].ID
		}
		return result[a].Score > result[b].Score
	})
	if len(result) > limit {
		result = result[:limit]
	}
	for n := range result {
		result[n].Rank = n + 1
	}
	return result, degraded, nil
}

func matches(doc Document, filters Filters) bool {
	if filters.OrganizationID != "" {
		if doc.OrganizationID != "" {
			if doc.OrganizationID != filters.OrganizationID {
				return false
			}
		} else if !strings.HasPrefix(doc.ID, filters.OrganizationID+"/") {
			return false
		}
	}
	if filters.HasScripts != nil && doc.HasScripts != *filters.HasScripts {
		return false
	}
	if len(filters.Repositories) > 0 && !contains(filters.Repositories, doc.RepositoryID) {
		return false
	}
	if len(filters.TrustLevels) > 0 && !contains(filters.TrustLevels, doc.TrustLevel) {
		return false
	}
	for key, value := range filters.Metadata {
		if doc.Metadata[key] != value {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (i *Index) lexicalSearch(query string, depth int) ([]string, error) {
	// Queries come from natural-language MCP input. MatchQuery prevents paths,
	// punctuation, and other ordinary prose from being interpreted as Bleve
	// query-string operators such as fuzzy-query syntax.
	req := bleve.NewSearchRequestOptions(bleve.NewMatchQuery(query), depth, 0, false)
	res, err := i.lexical.Search(req)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(res.Hits))
	for _, hit := range res.Hits {
		if doc, ok := i.docs[hit.ID]; ok && doc.Searchable {
			ids = append(ids, hit.ID)
		}
	}
	return ids, nil
}
func (i *Index) vectorSearch(query string, depth int) ([]string, bool) {
	degraded := i.embeddingDegraded
	if i.embedder == nil {
		return nil, true
	}
	i.observeEmbedding()
	vector, err := i.embedder.Embed(query)
	if err != nil {
		return nil, true
	}
	type scored struct {
		id    string
		score float64
	}
	all := []scored{}
	for id, doc := range i.docs {
		if !doc.Searchable || len(doc.Vector) == 0 {
			continue
		}
		all = append(all, scored{id, cosine(vector, doc.Vector)})
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].score == all[b].score {
			return all[a].id < all[b].id
		}
		return all[a].score > all[b].score
	})
	if len(all) > depth {
		all = all[:depth]
	}
	ids := make([]string, len(all))
	for n := range all {
		ids[n] = all[n].id
	}
	return ids, degraded
}
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, aa, bb float64
	for n := range a {
		x, y := float64(a[n]), float64(b[n])
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}
func routingText(doc Document) string {
	var b strings.Builder
	b.WriteString("name: " + doc.Name + "\ndescription: " + doc.Description + "\ncompatibility: " + doc.Compatibility + "\n")
	keys := make([]string, 0, len(doc.Metadata))
	for k := range doc.Metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(k + ": " + doc.Metadata[k] + "\n")
	}
	return b.String()
}
func matchedFields(doc Document, query string) []string {
	q := strings.ToLower(query)
	fields := []struct{ name, value string }{{"name", doc.Name}, {"description", doc.Description}, {"compatibility", doc.Compatibility}}
	var result []string
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f.value), q) {
			result = append(result, f.name)
		}
	}
	if len(result) == 0 {
		result = []string{"routing_document"}
	}
	return result
}
