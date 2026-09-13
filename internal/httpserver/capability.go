package httpserver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var capabilityServices sync.Map // map[*Server]*capability.Service

func (s *Server) ConfigureCapabilities(service *capability.Service) {
	if s == nil {
		return
	}
	if service == nil {
		capabilityServices.Delete(s)
		return
	}
	capabilityServices.Store(s, service)
}

type capabilityScopeInput struct {
	Namespace  string `json:"namespace,omitempty"`
	Repository string `json:"repository,omitempty"`
}

type searchCapabilitiesInput struct {
	Query   string               `json:"query"`
	Context string               `json:"context,omitempty"`
	Scope   capabilityScopeInput `json:"scope,omitempty"`
	Limit   int                  `json:"limit,omitempty"`
	Filters struct {
		TrustLevels []string          `json:"trust_levels,omitempty"`
		HasScripts  *bool             `json:"has_scripts,omitempty"`
		Metadata    map[string]string `json:"metadata,omitempty"`
	} `json:"filters,omitempty"`
}

type capabilityCandidate struct {
	CandidateID string                `json:"candidate_id"`
	Capability  capability.Descriptor `json:"capability"`
	Ranking     search.Hit            `json:"ranking"`
}

type searchCapabilitiesOutput struct {
	QueryID    string                `json:"query_id"`
	Degraded   map[string]bool       `json:"degraded"`
	Candidates []capabilityCandidate `json:"candidates"`
}

type describeCapabilityInput struct {
	CandidateID string               `json:"candidate_id"`
	Scope       capabilityScopeInput `json:"scope,omitempty"`
}

type describeCapabilityOutput struct {
	Detail                 capability.Detail `json:"detail"`
	MaterializeCandidateID string            `json:"materialize_candidate_id,omitempty"`
}

func addCapabilityTools(server *mcp.Server, app *Server) {
	if server == nil || app == nil {
		return
	}
	var service *capability.Service
	if value, ok := capabilityServices.Load(app); ok {
		service, _ = value.(*capability.Service)
	} else if app.search != nil {
		// Existing sources are central by default, so vNext capability discovery
		// is available without requiring a migration. A configured capability
		// service replaces this projection when repository-local policies exist.
		service, _ = capability.New(app.search, nil)
	}
	if service == nil {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_capabilities",
		Description: "Experimental scoped capability discovery. Returns compact skill/playbook descriptors only; repository-local capabilities are visible only in the exact supplied scope and are never boosted merely for being local.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input searchCapabilitiesInput) (*mcp.CallToolResult, searchCapabilitiesOutput, error) {
		return app.searchCapabilitiesTool(ctx, service, input)
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_capability",
		Description: "Describe one explicitly selected capability candidate and its immutable provenance. This never substitutes a successor, installs content, or executes a tool.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input describeCapabilityInput) (*mcp.CallToolResult, describeCapabilityOutput, error) {
		return app.describeCapabilityTool(ctx, service, input)
	})
}

func (s *Server) searchCapabilitiesTool(ctx context.Context, service *capability.Service, input searchCapabilitiesInput) (*mcp.CallToolResult, searchCapabilitiesOutput, error) {
	if len(input.Query) > 4000 || len(input.Context) > 8000 {
		return nil, searchCapabilitiesOutput{}, fmt.Errorf("query or context exceeds limit")
	}
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	scope, err := capability.NewScope(organizationID, input.Scope.Namespace, input.Scope.Repository)
	if err != nil {
		return nil, searchCapabilitiesOutput{}, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = s.defaultLimit
		if limit <= 0 {
			limit = 5
		}
	}
	maxLimit := s.maxLimit
	if maxLimit <= 0 {
		maxLimit = 10
	}
	if limit < 1 || limit > maxLimit {
		return nil, searchCapabilitiesOutput{}, fmt.Errorf("limit must be between 1 and %d", maxLimit)
	}
	lexicalDepth, vectorDepth, rrfK := s.lexicalDepth, s.vectorDepth, s.rrfK
	if lexicalDepth <= 0 {
		lexicalDepth = 50
	}
	if vectorDepth <= 0 {
		vectorDepth = 50
	}
	if rrfK <= 0 {
		rrfK = 60
	}
	query := input.Query
	if input.Context != "" {
		query += "\n" + input.Context
	}
	results, degraded, err := service.Search(query, lexicalDepth, vectorDepth, limit, rrfK, scope, search.Filters{
		TrustLevels: input.Filters.TrustLevels,
		HasScripts:  input.Filters.HasScripts,
		Metadata:    input.Filters.Metadata,
	})
	if err != nil {
		return nil, searchCapabilitiesOutput{}, err
	}
	queryID := fmt.Sprintf("cap_%x", sha256.Sum256([]byte(input.Query+"\x00"+input.Context+"\x00"+scope.Namespace+"\x00"+scope.Repository+"\x00"+time.Now().UTC().Format(time.RFC3339Nano))))
	out := searchCapabilitiesOutput{
		QueryID:    queryID,
		Degraded:   map[string]bool{"embedding": degraded},
		Candidates: make([]capabilityCandidate, 0, len(results)),
	}
	for _, result := range results {
		token, err := s.signer.Sign(candidate.Payload{
			Version: 1, OrganizationID: organizationID, RevisionID: result.Capability.Provenance.RevisionID,
			QueryID: queryID, IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(30 * time.Minute).Unix(),
		})
		if err != nil {
			return nil, searchCapabilitiesOutput{}, err
		}
		out.Candidates = append(out.Candidates, capabilityCandidate{CandidateID: token, Capability: result.Capability, Ranking: result.Ranking})
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Found %d scoped capability candidate(s). Explicitly select before describing or materializing.", len(out.Candidates))}}}, out, nil
}

func (s *Server) describeCapabilityTool(ctx context.Context, service *capability.Service, input describeCapabilityInput) (*mcp.CallToolResult, describeCapabilityOutput, error) {
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	if input.CandidateID == "" {
		return nil, describeCapabilityOutput{}, fmt.Errorf("candidate_id is required")
	}
	payload, err := s.signer.Verify(input.CandidateID, organizationID, time.Now())
	if err != nil {
		return nil, describeCapabilityOutput{}, err
	}
	scope, err := capability.NewScope(organizationID, input.Scope.Namespace, input.Scope.Repository)
	if err != nil {
		return nil, describeCapabilityOutput{}, err
	}
	descriptor, err := service.Describe(payload.RevisionID, scope)
	if err != nil {
		return nil, describeCapabilityOutput{}, err
	}
	detail := capability.Detail{Descriptor: descriptor}
	if descriptor.Identity.Kind == capability.KindSkill {
		detail.MaterializeWith = "materialize_skill"
	}
	if s.catalogue != nil {
		info, err := s.catalogue.Revision(ctx, organizationID, payload.RevisionID)
		if err != nil {
			return nil, describeCapabilityOutput{}, err
		}
		if info.SkillID != descriptor.Identity.ID || info.Commit != descriptor.Provenance.Commit || info.Tree != descriptor.Provenance.Tree {
			return nil, describeCapabilityOutput{}, fmt.Errorf("capability provenance no longer matches selected revision")
		}
		detail.Descriptor.Source.URL = info.RepositoryURL
		detail.Descriptor.Source.RepositoryID = info.RepositoryID
		detail.Descriptor.Provenance.ArchiveSHA256TarGZ = info.ArchiveSHA256TarGZ
		detail.Descriptor.Provenance.ArchiveSHA256ZIP = info.ArchiveSHA256ZIP
		detail.PackageDigests = capability.PackageDigests{TarGZ: info.ArchiveSHA256TarGZ, ZIP: info.ArchiveSHA256ZIP}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Capability described from the explicitly selected immutable revision; no replacement or execution was performed."}}}, describeCapabilityOutput{Detail: detail, MaterializeCandidateID: input.CandidateID}, nil
}
