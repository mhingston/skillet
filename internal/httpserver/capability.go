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

func configuredCapabilityServiceFor(app *Server) *capability.Service {
	if app == nil {
		return nil
	}
	if value, ok := capabilityServices.Load(app); ok {
		service, _ := value.(*capability.Service)
		return service
	}
	return nil
}

func capabilityServiceFor(app *Server) *capability.Service {
	if service := configuredCapabilityServiceFor(app); service != nil {
		return service
	}
	if app != nil && app.search != nil {
		// Existing sources are central by default, so vNext capability discovery
		// is available without requiring a migration. A configured capability
		// service replaces this projection when scoped or non-skill sources exist.
		service, _ := capability.New(app.search, nil)
		return service
	}
	return nil
}

func (s *Server) validateCapabilityNewSelection(revisionID string) error {
	// Current discovery candidates are governed by the configured capability
	// service. Retained historical revisions are intentionally absent from that
	// routing index, so explicit version/range selection must revalidate the
	// immutable revision's source-controlled governance directly from catalogue
	// history instead of treating absence from discovery as a yank.
	service := configuredCapabilityServiceFor(s)
	if service != nil && service.HasRevision(revisionID) {
		return service.AllowsNewSelection(revisionID)
	}
	if s != nil && s.catalogue != nil {
		record, err := s.catalogue.RevisionGovernance(context.Background(), s.organizationID, revisionID)
		if err != nil {
			return fmt.Errorf("resolve capability governance: %w", err)
		}
		if record.TrustLevel != "" && record.TrustLevel != "approved" {
			return fmt.Errorf("capability revision is not approved for new selection")
		}
		if record.Status == capability.StatusYanked {
			return fmt.Errorf("yanked capability revision is unavailable for new selection")
		}
		return nil
	}
	if service == nil {
		return nil
	}
	return service.AllowsNewSelection(revisionID)
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
	service := capabilityServiceFor(app)
	if service == nil {
		return
	}
	mcp.AddTool(server, &mcp.Tool{Name: "search_capabilities", Description: "Search reusable task capabilities (skills, playbooks, and configured MCP tool metadata) in an explicit organization/namespace/repository scope. Scope filters eligibility and never boosts relevance. Returned MCP schemas are metadata only; Skillet does not execute discovered tools."}, func(ctx context.Context, req *mcp.CallToolRequest, input searchCapabilitiesInput) (*mcp.CallToolResult, searchCapabilitiesOutput, error) {
		return app.searchCapabilitiesTool(ctx, service, req, input)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "describe_capability", Description: "Describe one explicitly selected capability revision. Skill bodies remain behind materialize_skill; tool schemas are returned only as untrusted metadata and are not executed."}, func(ctx context.Context, req *mcp.CallToolRequest, input describeCapabilityInput) (*mcp.CallToolResult, describeCapabilityOutput, error) {
		return app.describeCapabilityTool(ctx, service, req, input)
	})
}

func (s *Server) searchCapabilitiesTool(ctx context.Context, service *capability.Service, _ *mcp.CallToolRequest, input searchCapabilitiesInput) (*mcp.CallToolResult, searchCapabilitiesOutput, error) {
	if service == nil {
		return nil, searchCapabilitiesOutput{}, fmt.Errorf("capability service is unavailable")
	}
	if len(input.Query) > 4000 || len(input.Context) > 8000 {
		return nil, searchCapabilitiesOutput{}, fmt.Errorf("query or context exceeds limit")
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
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	scope, err := capability.NewScope(organizationID, input.Scope.Namespace, input.Scope.Repository)
	if err != nil {
		return nil, searchCapabilitiesOutput{}, err
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
	trustLevels := input.Filters.TrustLevels
	if len(trustLevels) == 0 {
		trustLevels = []string{"approved"}
	}
	results, degraded, err := service.Search(query, lexicalDepth, vectorDepth, limit, rrfK, scope, search.Filters{
		TrustLevels: trustLevels,
		HasScripts:  input.Filters.HasScripts,
		Metadata:    input.Filters.Metadata,
	})
	if err != nil {
		return nil, searchCapabilitiesOutput{}, err
	}
	queryID := fmt.Sprintf("cap_%x", sha256.Sum256([]byte(query+"\x00"+scope.Organization+"\x00"+scope.Namespace+"\x00"+scope.Repository+time.Now().UTC().Format(time.RFC3339Nano))))
	out := searchCapabilitiesOutput{QueryID: queryID, Degraded: map[string]bool{"embedding": degraded}, Candidates: make([]capabilityCandidate, 0, len(results))}
	for _, result := range results {
		token, err := s.signer.Sign(candidate.Payload{Version: 1, OrganizationID: organizationID, RevisionID: result.Capability.Provenance.RevisionID, QueryID: queryID, IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(30 * time.Minute).Unix()})
		if err != nil {
			return nil, searchCapabilitiesOutput{}, err
		}
		out.Candidates = append(out.Candidates, capabilityCandidate{CandidateID: token, Capability: result.Capability, Ranking: result.Ranking})
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Found %d scoped capability candidate(s). Review and explicitly select one before progressive disclosure or materialisation.", len(out.Candidates))}}}, out, nil
}

func (s *Server) describeCapabilityTool(ctx context.Context, service *capability.Service, _ *mcp.CallToolRequest, input describeCapabilityInput) (*mcp.CallToolResult, describeCapabilityOutput, error) {
	if service == nil {
		return nil, describeCapabilityOutput{}, fmt.Errorf("capability service is unavailable")
	}
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	payload, err := s.signer.Verify(input.CandidateID, organizationID, time.Now())
	if err != nil {
		return nil, describeCapabilityOutput{}, err
	}
	scope, err := capability.NewScope(organizationID, input.Scope.Namespace, input.Scope.Repository)
	if err != nil {
		return nil, describeCapabilityOutput{}, err
	}
	detail, err := service.DescribeDetail(payload.RevisionID, scope)
	if err != nil {
		return nil, describeCapabilityOutput{}, err
	}
	out := describeCapabilityOutput{Detail: detail}
	if detail.Descriptor.Identity.Kind == capability.KindSkill {
		out.MaterializeCandidateID = input.CandidateID
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Capability detail disclosed for the explicitly selected immutable revision. Tool schemas remain untrusted metadata and no tool execution occurred."}}}, out, nil
}
