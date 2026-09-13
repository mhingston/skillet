package capability

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/search"
)

type Candidate struct {
	Capability Descriptor `json:"capability"`
	Ranking    search.Hit  `json:"ranking"`
}

type Service struct {
	index             *search.Index
	policies          map[string]SourcePolicy
	details           map[string]Detail
	governance        map[string]GovernanceRecord
	identityRevisions map[string]string
}

func New(index *search.Index, policies []SourcePolicy) (*Service, error) {
	if index == nil {
		return nil, fmt.Errorf("capability search index is required")
	}
	values := make(map[string]SourcePolicy, len(policies))
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
		policy.Maintainers = append([]string(nil), policy.Maintainers...)
		values[policy.RepositoryID] = policy
	}
	return &Service{
		index:             index,
		policies:          values,
		details:           map[string]Detail{},
		governance:        map[string]GovernanceRecord{},
		identityRevisions: map[string]string{},
	}, nil
}

// PrepareDocuments projects reserved publisher governance metadata out of the
// semantic routing document before indexing. Yanked revisions remain in the
// document set for explicit provenance/history but are not searchable.
func PrepareDocuments(docs []search.Document, policies []SourcePolicy) ([]search.Document, []GovernanceRecord, error) {
	policyBySource := make(map[string]SourcePolicy, len(policies))
	for _, policy := range policies {
		policyBySource[policy.RepositoryID] = policy
	}
	prepared := make([]search.Document, 0, len(docs))
	records := make([]GovernanceRecord, 0, len(docs))
	for _, original := range docs {
		doc := original
		policy := policyBySource[doc.RepositoryID]
		state, metadata, err := governance.Parse(doc.Metadata, governance.Defaults{Owner: policy.Owner, Maintainers: policy.Maintainers})
		if err != nil {
			return nil, nil, fmt.Errorf("capability %q governance: %w", doc.SkillID, err)
		}
		doc.Metadata = governance.RoutingMetadata(doc.Metadata)
		if state == StatusYanked {
			doc.Searchable = false
		}
		prepared = append(prepared, doc)
		records = append(records, GovernanceRecord{RevisionID: doc.ID, IdentityID: doc.SkillID, Status: state, Metadata: metadata})
	}
	return prepared, records, nil
}

// RegisterGovernance attaches catalogue-control state to immutable routing
// revisions. The operation validates the complete batch before mutating the
// service so unresolved/invalid successor guidance cannot partially corrupt
// catalogue presentation.
func (s *Service) RegisterGovernance(records []GovernanceRecord) error {
	next := make(map[string]GovernanceRecord, len(s.governance)+len(records))
	for key, value := range s.governance {
		next[key] = value
	}
	for _, record := range records {
		if record.RevisionID == "" || record.IdentityID == "" {
			return fmt.Errorf("governance record requires revision and stable identity")
		}
		if !governance.ValidState(record.Status) {
			return fmt.Errorf("capability %q has invalid governance state %q", record.IdentityID, record.Status)
		}
		doc, ok := s.index.Document(record.RevisionID)
		if !ok {
			return fmt.Errorf("governance revision %q is not present in routing index", record.RevisionID)
		}
		if doc.SkillID != record.IdentityID {
			return fmt.Errorf("governance identity %q does not match routing document %q", record.IdentityID, doc.SkillID)
		}
		if record.Metadata.ReplacedBy != "" && record.Status != StatusDeprecated {
			return fmt.Errorf("replacement guidance for %q requires deprecated state", record.IdentityID)
		}
		if _, exists := next[record.RevisionID]; exists {
			return fmt.Errorf("duplicate governance revision %q", record.RevisionID)
		}
		next[record.RevisionID] = record
	}

	resolved, err := s.validateReplacementGuidance(next)
	if err != nil {
		return err
	}
	s.governance = resolved
	return nil
}

func (s *Service) validateReplacementGuidance(records map[string]GovernanceRecord) (map[string]GovernanceRecord, error) {
	byIdentity := map[string]search.Document{}
	for _, doc := range s.index.List(search.Filters{}) {
		if doc.SkillID != "" {
			byIdentity[doc.SkillID] = doc
		}
	}
	out := make(map[string]GovernanceRecord, len(records))
	for revisionID, record := range records {
		record.Metadata.ReplacementResolved = false
		if record.Metadata.ReplacedBy != "" {
			source, ok := s.index.Document(revisionID)
			if !ok {
				return nil, fmt.Errorf("governance source revision %q disappeared", revisionID)
			}
			targetID := record.Metadata.ReplacedBy
			if targetID == record.IdentityID {
				return nil, fmt.Errorf("capability %q cannot replace itself", record.IdentityID)
			}
			if !strings.HasPrefix(targetID, "mcp-tool:") && strings.Contains(targetID, "/") {
				organization, _, _ := strings.Cut(targetID, "/")
				if organization != source.OrganizationID {
					return nil, fmt.Errorf("replacement %q escapes organization %q", targetID, source.OrganizationID)
				}
			}
			if target, ok := byIdentity[targetID]; ok {
				if target.OrganizationID != source.OrganizationID {
					return nil, fmt.Errorf("replacement %q crosses organization boundary", targetID)
				}
				if !replacementScopeAllowed(s.ScopeForDocument(source), s.ScopeForDocument(target)) {
					return nil, fmt.Errorf("replacement %q is not visible throughout source scope", targetID)
				}
				record.Metadata.ReplacementResolved = true
			}
		}
		out[revisionID] = record
	}
	return out, nil
}

func replacementScopeAllowed(source, target Scope) bool {
	if source.Organization != target.Organization {
		return false
	}
	if source.Namespace == "" {
		return target.Namespace == "" && target.Repository == ""
	}
	if source.Repository == "" {
		return target.Namespace == "" || target.Namespace == source.Namespace && target.Repository == ""
	}
	if target.Namespace == "" {
		return true
	}
	if target.Namespace != source.Namespace {
		return false
	}
	return target.Repository == "" || target.Repository == source.Repository
}

// RegisterDetails attaches progressively disclosed metadata to routing
// documents already present in the capability index. It is intended for
// metadata-only capability kinds such as MCP tools. Registration never adds
// an execution function.
func (s *Service) RegisterDetails(details []Detail) error {
	for _, detail := range details {
		descriptor := detail.Descriptor
		revisionID := descriptor.Provenance.RevisionID
		if revisionID == "" || descriptor.Identity.ID == "" || descriptor.Identity.Kind == "" {
			return fmt.Errorf("capability detail requires revision, stable identity, and kind")
		}
		if !governance.ValidState(descriptor.Status) {
			return fmt.Errorf("capability detail %q has invalid state %q", revisionID, descriptor.Status)
		}
		doc, ok := s.index.Document(revisionID)
		if !ok {
			return fmt.Errorf("capability detail revision %q is not present in routing index", revisionID)
		}
		if _, exists := s.details[revisionID]; exists {
			return fmt.Errorf("duplicate capability detail revision %q", revisionID)
		}
		if previous, exists := s.identityRevisions[descriptor.Identity.ID]; exists && previous != revisionID {
			return fmt.Errorf("stable capability identity %q aliases revisions %q and %q", descriptor.Identity.ID, previous, revisionID)
		}
		if descriptor.Identity.ID != doc.SkillID {
			return fmt.Errorf("capability detail %q identity does not match routing document", revisionID)
		}
		if descriptor.Source.RepositoryID != doc.RepositoryID {
			return fmt.Errorf("capability detail %q source does not match routing document", revisionID)
		}
		if descriptor.Scope != s.ScopeForDocument(doc) {
			return fmt.Errorf("capability detail %q scope does not match source policy", revisionID)
		}
		if descriptor.Identity.Kind == KindTool && detail.Tool == nil {
			return fmt.Errorf("tool capability %q is missing tool detail", revisionID)
		}
		if descriptor.Identity.Kind != KindTool && detail.Tool != nil {
			return fmt.Errorf("non-tool capability %q cannot carry MCP tool detail", revisionID)
		}
		if descriptor.Status == StatusYanked && doc.Searchable {
			return fmt.Errorf("yanked capability %q cannot remain searchable", revisionID)
		}
		detail.Descriptor.Metadata = governance.RoutingMetadata(detail.Descriptor.Metadata)
		s.details[revisionID] = detail
		s.identityRevisions[descriptor.Identity.ID] = revisionID
	}
	return nil
}

func (s *Service) policyForDocument(doc search.Document) SourcePolicy {
	if policy, ok := s.policies[doc.RepositoryID]; ok {
		return policy
	}
	return SourcePolicy{RepositoryID: doc.RepositoryID, Scope: Scope{Organization: doc.OrganizationID}}
}

// ScopeForDocument returns configured source scope. Sources without an
// explicit policy remain organisation-wide for v1 compatibility.
func (s *Service) ScopeForDocument(doc search.Document) Scope {
	return s.policyForDocument(doc).Scope
}

func (s *Service) visible(doc search.Document, request Scope) bool {
	if !doc.Searchable || doc.OrganizationID != request.Organization {
		return false
	}
	descriptor := s.descriptorForDocument(doc)
	if descriptor.Status == StatusYanked {
		return false
	}
	return descriptor.Scope.Allows(request)
}

func (s *Service) descriptorForDocument(doc search.Document) Descriptor {
	var descriptor Descriptor
	if detail, ok := s.details[doc.ID]; ok {
		descriptor = detail.Descriptor
	} else {
		descriptor = descriptorFromDocument(doc, s.ScopeForDocument(doc))
	}
	descriptor.Scope = s.ScopeForDocument(doc)
	descriptor.Metadata = governance.RoutingMetadata(descriptor.Metadata)
	if !governance.ValidState(descriptor.Status) {
		descriptor.Status = StatusActive
	}

	policy := s.policyForDocument(doc)
	control := governance.Metadata{Owner: policy.Owner, Maintainers: append([]string(nil), policy.Maintainers...)}
	if record, ok := s.governance[doc.ID]; ok {
		descriptor.Status = record.Status
		control = record.Metadata
		if control.Owner == "" {
			control.Owner = policy.Owner
		}
		if len(control.Maintainers) == 0 {
			control.Maintainers = append([]string(nil), policy.Maintainers...)
		}
	}
	control.Visibility = visibilityForScope(descriptor.Scope)
	descriptor.Governance = control
	return descriptor
}

func visibilityForScope(scope Scope) governance.Visibility {
	if scope.Repository != "" {
		return governance.VisibilityRepository
	}
	if scope.Namespace != "" {
		return governance.VisibilityNamespace
	}
	return governance.VisibilityOrganization
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
			visible[doc.ID] = s.descriptorForDocument(doc)
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
// selected immutable revision, provided it remains eligible for new use in the
// request scope. Exact historical lock restoration is handled separately and
// never follows successor guidance.
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
	return s.descriptorForDocument(doc), nil
}

// DescribeDetail progressively discloses kind-specific detail only after an
// explicit revision has been selected and re-authorized for the same scope.
func (s *Service) DescribeDetail(revisionID string, request Scope) (Detail, error) {
	descriptor, err := s.Describe(revisionID, request)
	if err != nil {
		return Detail{}, err
	}
	if detail, ok := s.details[revisionID]; ok {
		detail.Descriptor = descriptor
		return detail, nil
	}
	return Detail{Descriptor: descriptor}, nil
}

func descriptorFromDocument(doc search.Document, scope Scope) Descriptor {
	metadata := governance.RoutingMetadata(doc.Metadata)
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
		policy := s.policies[key]
		policy.Maintainers = append([]string(nil), policy.Maintainers...)
		out = append(out, policy)
	}
	return out
}
