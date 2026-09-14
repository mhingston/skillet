package httpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/evidence"
	"github.com/mhingston/skillet/internal/proposal"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var proposalStores sync.Map // map[*Server]*proposal.Store

type prepareImprovementProposalInput struct {
	CandidateID     string `json:"candidate_id" jsonschema:"Deterministic improvement candidate identifier"`
	RevisionID      string `json:"revision_id" jsonschema:"Exact immutable base revision identifier"`
	IntendedOutcome string `json:"intended_outcome,omitempty" jsonschema:"Optional bounded intended outcome; defaults to a conservative evidence-derived outcome"`
	CorrelationID   string `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type improvementProposalIDInput struct {
	ProposalID string `json:"proposal_id" jsonschema:"Skillet-owned improvement proposal identifier"`
}

type listImprovementProposalsInput struct {
	RevisionID string `json:"revision_id" jsonschema:"Exact immutable base revision identifier"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum proposals to return (1-100; default 25)"`
}

type attachImprovementProposalInput struct {
	ProposalID        string                        `json:"proposal_id" jsonschema:"Skillet-owned improvement proposal identifier"`
	BaseRevisionID    string                        `json:"base_revision_id" jsonschema:"Exact immutable revision the proposed artifact was based on"`
	Patch             string                        `json:"patch,omitempty" jsonschema:"Optional bounded git unified diff scoped to the capability source path"`
	ExternalReference string                        `json:"external_reference,omitempty" jsonschema:"Optional HTTPS external review/PR reference without embedded credentials"`
	Verification      []proposal.VerificationResult `json:"verification" jsonschema:"Machine-readable results for the fixed verification obligations in the handoff"`
}

func addProposalTools(server *mcp.Server, app *Server) {
	if server == nil || app == nil || app.catalogue == nil {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "prepare_improvement_proposal",
		Description: "Persist a reviewable proposal pinned to one immutable evidence candidate/base revision and return a deterministic bounded agent handoff. This never edits source or active capability state.",
	}, app.prepareImprovementProposalTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_improvement_proposal",
		Description: "Read one authorised reviewable proposal, including immutable provenance, bounded handoff, artifact, verification history, and stale state.",
	}, app.getImprovementProposalTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_improvement_proposals",
		Description: "List authorised proposals for one exact immutable capability revision.",
	}, app.listImprovementProposalsTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "attach_improvement_proposal",
		Description: "Attach a bounded patch or external review reference plus verification results. Ready-for-review is derived only when every fixed obligation passes; Skillet does not publish or merge the change.",
	}, app.attachImprovementProposalTool)
}

func (s *Server) prepareImprovementProposalTool(ctx context.Context, _ *mcp.CallToolRequest, input prepareImprovementProposalInput) (*mcp.CallToolResult, proposal.Proposal, error) {
	input.CandidateID = strings.TrimSpace(input.CandidateID)
	input.RevisionID = strings.TrimSpace(input.RevisionID)
	if input.CandidateID == "" || input.RevisionID == "" {
		return nil, proposal.Proposal{}, fmt.Errorf("candidate_id and revision_id are required")
	}
	organizationID, actorID := s.proposalActor(ctx)
	info, err := s.catalogue.Revision(ctx, organizationID, input.RevisionID)
	if err != nil {
		return nil, proposal.Proposal{}, err
	}
	resource := s.capabilityAuthorizationResource(organizationID, info)
	if err := s.authorize(ctx, authz.ActionProposalPrepare, resource); err != nil {
		return nil, proposal.Proposal{}, err
	}
	if err := s.authorize(ctx, authz.ActionEvidenceReview, resource); err != nil {
		return nil, proposal.Proposal{}, err
	}
	if err := s.authorize(ctx, authz.ActionCapabilityDescribe, resource); err != nil {
		return nil, proposal.Proposal{}, err
	}
	candidate, err := s.proposalCandidate(ctx, organizationID, input.RevisionID, input.CandidateID)
	if err != nil {
		return nil, proposal.Proposal{}, err
	}
	store, err := s.proposalStore(ctx)
	if err != nil {
		return nil, proposal.Proposal{}, err
	}
	item, err := store.Prepare(ctx, proposal.PrepareInput{
		OrganizationID: organizationID,
		ActorID: actorID,
		CorrelationID: strings.TrimSpace(input.CorrelationID),
		Candidate: candidate,
		IntendedOutcome: input.IntendedOutcome,
	})
	if err != nil {
		return nil, proposal.Proposal{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "improvement_proposal_prepared", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "skill_id": item.CapabilityID,
		"revision_id": item.Base.RevisionID, "proposal_id": item.ID, "candidate_id": item.CandidateID,
		"request_id": item.CorrelationID,
	}); err != nil {
		return nil, proposal.Proposal{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Prepared immutable-base improvement proposal. The returned handoff is data for an external agent/human workflow; no source or active capability state was changed."}}}, item, nil
}

func (s *Server) getImprovementProposalTool(ctx context.Context, _ *mcp.CallToolRequest, input improvementProposalIDInput) (*mcp.CallToolResult, proposal.Proposal, error) {
	organizationID, _ := s.proposalActor(ctx)
	resource, err := s.proposalResource(ctx, organizationID, strings.TrimSpace(input.ProposalID))
	if err != nil {
		return nil, proposal.Proposal{}, err
	}
	if err := s.authorize(ctx, authz.ActionProposalRead, resource); err != nil {
		return nil, proposal.Proposal{}, err
	}
	store, err := s.proposalStore(ctx)
	if err != nil {
		return nil, proposal.Proposal{}, err
	}
	item, err := store.Get(ctx, organizationID, strings.TrimSpace(input.ProposalID))
	if err != nil {
		return nil, proposal.Proposal{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned reviewable improvement proposal. Status is review state only and never changes capability retrieval/governance."}}}, item, nil
}

func (s *Server) listImprovementProposalsTool(ctx context.Context, _ *mcp.CallToolRequest, input listImprovementProposalsInput) (*mcp.CallToolResult, []proposal.Proposal, error) {
	input.RevisionID = strings.TrimSpace(input.RevisionID)
	if input.RevisionID == "" {
		return nil, nil, fmt.Errorf("revision_id is required")
	}
	limit := input.Limit
	if limit == 0 {
		limit = 25
	}
	organizationID, _ := s.proposalActor(ctx)
	info, err := s.catalogue.Revision(ctx, organizationID, input.RevisionID)
	if err != nil {
		return nil, nil, err
	}
	resource := s.capabilityAuthorizationResource(organizationID, info)
	if err := s.authorize(ctx, authz.ActionProposalRead, resource); err != nil {
		return nil, nil, err
	}
	store, err := s.proposalStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	items, err := store.ListRevision(ctx, organizationID, input.RevisionID, limit)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Returned %d reviewable proposal(s) for the exact immutable revision.", len(items))}}}, items, nil
}

func (s *Server) attachImprovementProposalTool(ctx context.Context, _ *mcp.CallToolRequest, input attachImprovementProposalInput) (*mcp.CallToolResult, proposal.Proposal, error) {
	organizationID, actorID := s.proposalActor(ctx)
	resource, err := s.proposalResource(ctx, organizationID, strings.TrimSpace(input.ProposalID))
	if err != nil {
		return nil, proposal.Proposal{}, err
	}
	if err := s.authorize(ctx, authz.ActionProposalAttach, resource); err != nil {
		return nil, proposal.Proposal{}, err
	}
	store, err := s.proposalStore(ctx)
	if err != nil {
		return nil, proposal.Proposal{}, err
	}
	item, err := store.Attach(ctx, proposal.AttachInput{
		OrganizationID: organizationID,
		ActorID: actorID,
		ProposalID: strings.TrimSpace(input.ProposalID),
		BaseRevisionID: strings.TrimSpace(input.BaseRevisionID),
		Patch: input.Patch,
		ExternalReference: input.ExternalReference,
		Results: input.Verification,
	})
	if err != nil {
		return nil, proposal.Proposal{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "improvement_proposal_artifact_attached", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "skill_id": item.CapabilityID,
		"revision_id": item.Base.RevisionID, "proposal_id": item.ID, "patch_sha256": item.PatchSHA256,
		"status": item.Status,
	}); err != nil {
		return nil, proposal.Proposal{}, err
	}
	text := "Proposal artifact and verification evidence recorded; proposal remains draft because one or more fixed obligations did not pass."
	if item.Status == proposal.StatusReadyForReview {
		text = "Proposal artifact passed every fixed verification obligation and is ready for external human/source-repository review. Skillet has not published or merged it."
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, item, nil
}

func (s *Server) proposalCandidate(ctx context.Context, organizationID, revisionID, candidateID string) (evidence.Candidate, error) {
	result, err := evidence.New(s.catalogue).Candidates(ctx, organizationID, evidence.Query{RevisionID: revisionID})
	if err != nil {
		return evidence.Candidate{}, err
	}
	for _, candidate := range result.Candidates {
		if candidate.ID == candidateID {
			return candidate, nil
		}
	}
	return evidence.Candidate{}, fmt.Errorf("improvement candidate is unavailable for the exact revision")
}

func (s *Server) proposalStore(ctx context.Context) (*proposal.Store, error) {
	if existing, ok := proposalStores.Load(s); ok {
		store, _ := existing.(*proposal.Store)
		if store != nil {
			return store, nil
		}
	}
	if s == nil || s.catalogue == nil {
		return nil, fmt.Errorf("proposal persistence is unavailable")
	}
	store, err := proposal.New(ctx, s.catalogue)
	if err != nil {
		return nil, err
	}
	actual, _ := proposalStores.LoadOrStore(s, store)
	resolved, _ := actual.(*proposal.Store)
	if resolved == nil {
		return nil, fmt.Errorf("proposal persistence is unavailable")
	}
	return resolved, nil
}

func (s *Server) proposalActor(ctx context.Context) (string, string) {
	organizationID, actorID := s.organizationID, "development"
	if identity, ok := Identity(ctx); ok {
		organizationID, actorID = identity.OrganizationID, identity.Subject
	}
	return organizationID, actorID
}

func (s *Server) proposalResource(ctx context.Context, organizationID, proposalID string) (authz.Resource, error) {
	if strings.TrimSpace(proposalID) == "" || s.catalogue == nil || s.catalogue.DB == nil {
		return authz.Resource{}, fmt.Errorf("proposal id is required")
	}
	var capabilityID, revisionID string
	if err := s.catalogue.DB.QueryRowContext(ctx, `SELECT capability_id, base_revision_id FROM improvement_proposals WHERE organization_id=? AND id=?`, organizationID, proposalID).Scan(&capabilityID, &revisionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authz.Resource{}, fmt.Errorf("proposal not found")
		}
		return authz.Resource{}, err
	}
	info, err := s.catalogue.Revision(ctx, organizationID, revisionID)
	if err != nil {
		return authz.Resource{}, err
	}
	resource := s.capabilityAuthorizationResource(organizationID, info)
	resource.ID = capabilityID
	return resource, nil
}
