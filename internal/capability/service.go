package capability

import (
	"fmt"
	"sort"

	"github.com/mhingston/skillet/internal/search"
)

type Candidate struct {
	Capability Descriptor `json:"capability"`
	Ranking    search.Hit  `json:"ranking"`
}

type Service struct {
	index    *search.Index
	policies map[string]Scope
}

func New(index *search.Index, policies []SourcePolicy) (*Service, error) {
	if index == nil {
		return nil, fmt.Errorf("capability search index is required")
	}
	values := make(map[string]Scope, len(policies))
	for _, policy := range policies {
		if policy.RepositoryID == "" {
			return nil, fmt.Errorf("capability source policy repository id is required")
		}
		if err := policy.Scope.Validate(); err != nil {
			return nil, fmt.Errorf("capability source %q: %w", policy.RepositoryID, err)
		}
		if _, exists := values[policy.RepositoryID]; exists {
			return nil, fmt.Errorf("duplicate capability source policy %q", policy.RepositoryID)
		}
		values[policy.RepositoryID] = policy.Scope
	}
	return &Service{index: index, policies: values}, nil
}

// ScopeForDocument returns configured source scope. Sources without an
// explicit policy remain organisation-wide for v1 compatibility.
func (s *Service) ScopeForDocument(doc search.Document) Scope {
	if scope, ok := s.policies[doc.RepositoryID]; ok {
		return scope
	}
	return Scope{Organization: doc.OrganizationID}
}

func (s *Service) visible(doc search.Document, request Scope) bool {
	if !doc.Searchable || doc.OrganizationID != request.Organization {
		return false
	}
	return s.ScopeForDocument(doc).Allows(request)
}

func (s *Service) List(request Scope, filters search.Filters) ([]search.Document, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	filters.OrganizationID = request.Organization
	docs := s.index.List(filters)
	out := make([]search.Document, 0, len(docs))
	for _, doc := range docs {
		if s.visible(doc, request) {
			out = append(out, doc)
		}
	}
	return out, nil
}

// Search applies scope as an eligibility constraint before relevance ranking.
// Scope therefore cannot boost local candidates, and ineligible repositories
// cannot perturb RRF/vector ranks or appear in semantic-neighbour evidence.
func (s *Service) Search(query string, lexicalDepth, vectorDepth, limit, rrfK int, request Scope, filters search.Filters) ([]Candidate, bool, error) {
	if err := request.Validate(); err != nil {
		return nil, false, err
	}
	if limit < 1 {
		return nil, false, fmt.Errorf("limit must be positive")
	}
	filters.OrganizationID = request.Organization
	all := s.index.List(filters)
	visible := make(map[string]Descriptor, len(all))
	eligibleRepositories := make(map[string]struct{})
	for _, doc := range all {
		if s.visible(doc, request) {
			visible[doc.ID] = descriptorFromDocument(doc, s.ScopeForDocument(doc))
			eligibleRepositories[doc.RepositoryID] = struct{}{}
		}
	}
	if len(visible) == 0 {
		return []Candidate{}, false, nil
	}
	filters.Repositories = filters.Repositories[:0]
	for repositoryID := range eligibleRepositories {
		filters.Repositories = append(filters.Repositories, repositoryID)
	}
	sort.Strings(filters.Repositories)

	// Search the full pre-scope pool before compacting eligible ranks. This
	// prevents an ineligible high-ranking source from crowding an eligible
	// candidate out of the retrieval depth while still ensuring its original
	// rank cannot affect capability RRF scores.
	candidatePoolCount := len(all)
	if lexicalDepth < candidatePoolCount {
		lexicalDepth = candidatePoolCount
	}
	if vectorDepth < candidatePoolCount {
		vectorDepth = candidatePoolCount
	}
	hits, degraded, err := s.index.SearchEligible(query, lexicalDepth, vectorDepth, len(visible), rrfK, filters)
	if err != nil {
		return nil, degraded, err
	}
	out := make([]Candidate, 0, limit)
	for _, hit := range hits {
		descriptor, ok := visible[hit.ID]
		if !ok {
			continue
		}
		neighbors := hit.SemanticNeighbors[:0]
		for _, neighbor := range hit.SemanticNeighbors {
			if _, ok := visible[neighbor.Skill.ID]; ok {
				neighbors = append(neighbors, neighbor)
			}
		}
		hit.SemanticNeighbors = neighbors
		hit.Rank = len(out) + 1
		out = append(out, Candidate{Capability: descriptor, Ranking: hit})
		if len(out) == limit {
			break
		}
	}
	return out, degraded, nil
}

// Describe returns the compact capability description for an explicitly
// selected immutable revision, provided it is visible in request scope.
func (s *Service) Describe(revisionID string, request Scope) (Descriptor, error) {
	if err := request.Validate(); err != nil {
		return Descriptor{}, err
	}
	if revisionID == "" {
		return Descriptor{}, fmt.Errorf("revision id is required")
	}
	doc, ok := s.index.Document(revisionID)
	if !ok || !s.visible(doc, request) {
		return Descriptor{}, fmt.Errorf("capability revision not found in scope")
	}
	return descriptorFromDocument(doc, s.ScopeForDocument(doc)), nil
}

func descriptorFromDocument(doc search.Document, scope Scope) Descriptor {
	metadata := make(map[string]string, len(doc.Metadata))
	for key, value := range doc.Metadata {
		metadata[key] = value
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	return Descriptor{
		Identity:      Identity{ID: doc.SkillID, Kind: KindSkill},
		Name:          doc.Name,
		Description:   doc.Description,
		Version:       doc.Version,
		Compatibility: doc.Compatibility,
		Scope:         scope,
		Source:        Source{RepositoryID: doc.RepositoryID, Path: doc.Path},
		Provenance:    Provenance{RevisionID: doc.ID, Commit: doc.Commit, Tree: doc.Tree},
		TrustLevel:    doc.TrustLevel,
		Status:        StatusActive,
		Metadata:      metadata,
		HasScripts:    doc.HasScripts,
	}
}

// Policies returns a deterministic copy for diagnostics/tests.
func (s *Service) Policies() []SourcePolicy {
	keys := make([]string, 0, len(s.policies))
	for key := range s.policies {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]SourcePolicy, 0, len(keys))
	for _, key := range keys {
		out = append(out, SourcePolicy{RepositoryID: key, Scope: s.policies[key]})
	}
	return out
}
