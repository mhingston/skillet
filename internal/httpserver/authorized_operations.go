package httpserver

import (
	"context"
	"fmt"
	"time"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/lockfile"
	"github.com/mhingston/skillet/internal/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) authorizedSearchTool(ctx context.Context, req *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, searchOutput, error) {
	if authorizationPolicyFor(s) == nil {
		return s.searchTool(ctx, req, input)
	}
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	// This organisation-only check is an eligibility probe. ClaimsPolicy does
	// not treat it as authority for any namespace/repository; every ranked
	// candidate is re-authorized below from authoritative source provenance.
	if err := s.authorize(ctx, authz.ActionCapabilitySearch, authz.Resource{OrganizationID: organizationID}); err != nil {
		return nil, searchOutput{}, err
	}
	requestedLimit := input.Limit
	if requestedLimit == 0 {
		requestedLimit = s.defaultLimit
		if requestedLimit <= 0 {
			requestedLimit = 5
		}
	}
	maxLimit := s.maxLimit
	if maxLimit <= 0 {
		maxLimit = 10
	}
	if requestedLimit < 1 || requestedLimit > maxLimit {
		return nil, searchOutput{}, fmt.Errorf("limit must be between 1 and %d", maxLimit)
	}
	input.Limit = maxLimit
	result, out, err := s.searchTool(ctx, req, input)
	if err != nil {
		return result, out, err
	}
	filtered := make([]searchCandidate, 0, requestedLimit)
	for _, item := range out.Candidates {
		resource := s.capabilitySearchAuthorizationResource(organizationID, item.Skill)
		if err := s.authorize(ctx, authz.ActionCapabilitySearch, resource); err != nil {
			continue
		}
		filtered = append(filtered, item)
		if len(filtered) == requestedLimit {
			break
		}
	}
	out.Candidates = filtered
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Found %d candidate skill(s). Review them and materialize only selected candidates.", len(filtered))}}}, out, nil
}

func (s *Server) authorizedListSkillsTool(ctx context.Context, _ *mcp.CallToolRequest, input listSkillsInput) (*mcp.CallToolResult, listSkillsOutput, error) {
	if authorizationPolicyFor(s) == nil {
		return s.listSkillsTool(ctx, nil, input)
	}
	if input.Offset < 0 {
		return nil, listSkillsOutput{}, fmt.Errorf("offset must be non-negative")
	}
	limit := input.Limit
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > 100 {
		return nil, listSkillsOutput{}, fmt.Errorf("limit must be between 1 and 100")
	}
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	if err := s.authorize(ctx, authz.ActionCapabilitySearch, authz.Resource{OrganizationID: organizationID}); err != nil {
		return nil, listSkillsOutput{}, err
	}
	docs := s.search.List(search.Filters{OrganizationID: organizationID})
	allowed := make([]search.Document, 0, len(docs))
	for _, doc := range docs {
		resource := s.capabilitySearchAuthorizationResource(organizationID, doc)
		if err := s.authorize(ctx, authz.ActionCapabilitySearch, resource); err != nil {
			continue
		}
		doc.Vector = nil
		allowed = append(allowed, doc)
	}
	total := len(allowed)
	if input.Offset >= total {
		allowed = []search.Document{}
	} else {
		allowed = allowed[input.Offset:]
		if len(allowed) > limit {
			allowed = allowed[:limit]
		}
	}
	out := listSkillsOutput{Skills: allowed, Offset: input.Offset, Limit: limit, Total: total, HasMore: input.Offset+len(allowed) < total}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Listed %d of %d active approved skill(s).", len(allowed), total)}}}, out, nil
}

func (s *Server) authorizedResolveTool(ctx context.Context, req *mcp.CallToolRequest, input resolveSkillInput) (*mcp.CallToolResult, resolveSkillOutput, error) {
	if authorizationPolicyFor(s) == nil {
		return s.resolveTool(ctx, req, input)
	}
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	if (input.Version == "") == (input.Range == "") {
		return nil, resolveSkillOutput{}, fmt.Errorf("exactly one of version or range is required")
	}
	info, err := s.catalogue.ResolveVersion(ctx, organizationID, input.SkillID, input.Version, input.Range)
	if err != nil {
		return nil, resolveSkillOutput{}, err
	}
	if err := s.authorizeCapabilityDescription(ctx, organizationID, info); err != nil {
		return nil, resolveSkillOutput{}, err
	}
	return s.resolveTool(ctx, req, input)
}

func (s *Server) authorizedMaterializeTool(ctx context.Context, req *mcp.CallToolRequest, input materializeInput) (*mcp.CallToolResult, materializeOutput, error) {
	if authorizationPolicyFor(s) == nil {
		return s.materializeTool(ctx, req, input)
	}
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	modes := 0
	if input.CandidateID != "" {
		modes++
	}
	if input.Locked != nil {
		modes++
	}
	if input.Version != "" {
		modes++
	}
	if input.Range != "" {
		modes++
	}
	if modes != 1 || (input.Version != "" && input.Range != "") {
		return nil, materializeOutput{}, fmt.Errorf("exactly one of candidate_id, version, range, or locked is required")
	}

	if input.Locked != nil {
		if s.restorer == nil {
			return nil, materializeOutput{}, fmt.Errorf("locked restore is unavailable")
		}
		locked := input.Locked
		entry := lockfile.Entry{Name: "", Source: lockfile.Source{Type: "git", RepositoryID: locked.RepositoryID, Path: locked.Path}, Resolved: lockfile.Resolved{Commit: locked.Commit, Tree: locked.Tree}, Integrity: lockfile.Integrity{Algorithm: "sha256", Archive: locked.ArchiveSHA256, Format: locked.Format}}
		restorer := *s.restorer
		restorer.OrganizationID = organizationID
		info, err := restorer.ResolveRevision(ctx, locked.SkillID, entry)
		if err != nil {
			return nil, materializeOutput{}, err
		}
		if err := s.authorizeCapabilityMaterialization(ctx, organizationID, info); err != nil {
			return nil, materializeOutput{}, err
		}
		return s.materializeAuthorizedRevision(ctx, req, input, info)
	}

	var revisionID string
	if input.CandidateID != "" {
		payload, err := s.signer.Verify(input.CandidateID, organizationID, time.Now())
		if err != nil {
			return nil, materializeOutput{}, err
		}
		revisionID = payload.RevisionID
	} else {
		if input.SkillID == "" {
			return nil, materializeOutput{}, fmt.Errorf("skill_id is required for version materialization")
		}
		info, err := s.catalogue.ResolveVersion(ctx, organizationID, input.SkillID, input.Version, input.Range)
		if err != nil {
			return nil, materializeOutput{}, err
		}
		revisionID = info.RevisionID
	}
	info, err := s.catalogue.Revision(ctx, organizationID, revisionID)
	if err != nil {
		return nil, materializeOutput{}, err
	}
	if err := s.authorizeCapabilityMaterialization(ctx, organizationID, info); err != nil {
		return nil, materializeOutput{}, err
	}
	return s.materializeAuthorizedRevision(ctx, req, input, info)
}

// Compile-time guard: wrappers intentionally retain the existing signed
// candidate type in this file so candidate-token semantics remain explicit.
var _ candidate.Payload
