package httpserver

import (
	"context"
	"fmt"
	"strings"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/curriculum"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) createCurriculumProposalTool(ctx context.Context, _ *mcp.CallToolRequest, input createCurriculumProposalInput) (*mcp.CallToolResult, curriculum.Proposal, error) {
	organizationID, actorID := s.proposalActor(ctx)
	store, err := s.curriculumStore(ctx)
	if err != nil {
		return nil, curriculum.Proposal{}, err
	}
	gap, err := store.GetGap(ctx, organizationID, strings.TrimSpace(input.GapID))
	if err != nil {
		return nil, curriculum.Proposal{}, err
	}
	if err := s.authorizeCurriculumGap(ctx, organizationID, gap, authz.ActionCurriculumPropose); err != nil {
		return nil, curriculum.Proposal{}, err
	}
	item, err := store.CreateProposal(ctx, curriculum.CreateProposalInput{OrganizationID: organizationID, ActorID: actorID, CorrelationID: strings.TrimSpace(input.CorrelationID), GapID: input.GapID, Name: input.Name, Version: input.Version, Kind: input.Kind, Title: input.Title, Intent: input.Intent, ArtifactReference: input.ArtifactReference, ArtifactSHA256: input.ArtifactSHA256, Oracle: input.Oracle})
	if err != nil {
		return nil, curriculum.Proposal{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "curriculum_proposal_created", map[string]any{"actor_type": "agent_or_human", "actor_id": actorID, "capability_gap_id": item.GapID, "curriculum_proposal_id": item.ID, "proposal_revision": item.ProposalRevision, "proposal_kind": item.Kind, "proposal_audience": item.Audience, "request_id": item.CorrelationID}); err != nil {
		return nil, curriculum.Proposal{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Created an immutable versioned curriculum proposal. It is review evidence only and has not modified any eval suite or executed any task."}}}, item, nil
}

func (s *Server) getCurriculumProposalTool(ctx context.Context, _ *mcp.CallToolRequest, input curriculumProposalIDInput) (*mcp.CallToolResult, curriculum.Proposal, error) {
	organizationID, _ := s.proposalActor(ctx)
	store, err := s.curriculumStore(ctx)
	if err != nil {
		return nil, curriculum.Proposal{}, err
	}
	item, err := store.GetProposal(ctx, organizationID, strings.TrimSpace(input.ProposalID))
	if err != nil {
		return nil, curriculum.Proposal{}, err
	}
	gap, err := store.GetGap(ctx, organizationID, item.GapID)
	if err != nil {
		return nil, curriculum.Proposal{}, err
	}
	if err := s.authorizeCurriculumGap(ctx, organizationID, gap, authz.ActionCurriculumRead); err != nil {
		return nil, curriculum.Proposal{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned immutable curriculum proposal metadata and external artifact digest."}}}, item, nil
}

func (s *Server) reviewCurriculumProposalTool(ctx context.Context, _ *mcp.CallToolRequest, input reviewCurriculumProposalInput) (*mcp.CallToolResult, curriculum.Review, error) {
	organizationID, actorID := s.proposalActor(ctx)
	store, err := s.curriculumStore(ctx)
	if err != nil {
		return nil, curriculum.Review{}, err
	}
	proposal, err := store.GetProposal(ctx, organizationID, strings.TrimSpace(input.ProposalID))
	if err != nil {
		return nil, curriculum.Review{}, err
	}
	gap, err := store.GetGap(ctx, organizationID, proposal.GapID)
	if err != nil {
		return nil, curriculum.Review{}, err
	}
	if err := s.authorizeCurriculumGap(ctx, organizationID, gap, authz.ActionCurriculumReview); err != nil {
		return nil, curriculum.Review{}, err
	}
	item, err := store.ReviewProposal(ctx, curriculum.ReviewInput{OrganizationID: organizationID, ActorID: actorID, CorrelationID: strings.TrimSpace(input.CorrelationID), ProposalID: proposal.ID, Decision: input.Decision, Reference: input.Reference})
	if err != nil {
		return nil, curriculum.Review{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "curriculum_proposal_reviewed", map[string]any{"actor_type": "agent_or_human", "actor_id": actorID, "curriculum_proposal_id": proposal.ID, "review_id": item.ID, "decision": item.Decision, "review_reference": item.Reference, "request_id": item.CorrelationID}); err != nil {
		return nil, curriculum.Review{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Recorded explicit immutable review evidence. Accepting a proposal does not itself modify an eval suite."}}}, item, nil
}

func (s *Server) getCurriculumReviewTool(ctx context.Context, _ *mcp.CallToolRequest, input curriculumProposalIDInput) (*mcp.CallToolResult, curriculum.Review, error) {
	organizationID, _ := s.proposalActor(ctx)
	store, err := s.curriculumStore(ctx)
	if err != nil {
		return nil, curriculum.Review{}, err
	}
	proposal, err := store.GetProposal(ctx, organizationID, strings.TrimSpace(input.ProposalID))
	if err != nil {
		return nil, curriculum.Review{}, err
	}
	gap, err := store.GetGap(ctx, organizationID, proposal.GapID)
	if err != nil {
		return nil, curriculum.Review{}, err
	}
	if err := s.authorizeCurriculumGap(ctx, organizationID, gap, authz.ActionCurriculumRead); err != nil {
		return nil, curriculum.Review{}, err
	}
	item, err := store.GetReview(ctx, organizationID, proposal.ID)
	if err != nil {
		return nil, curriculum.Review{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned immutable explicit curriculum review evidence."}}}, item, nil
}

func (s *Server) createCurriculumEvalSuiteVersionTool(ctx context.Context, _ *mcp.CallToolRequest, input createCurriculumEvalSuiteVersionInput) (*mcp.CallToolResult, curriculum.EvalSuiteVersion, error) {
	organizationID, actorID := s.proposalActor(ctx)
	resource := authz.Resource{OrganizationID: organizationID, ID: "curriculum-eval-suite:" + strings.TrimSpace(input.Name)}
	if err := s.authorize(ctx, authz.ActionCurriculumEvolveSuite, resource); err != nil {
		return nil, curriculum.EvalSuiteVersion{}, err
	}
	store, err := s.curriculumStore(ctx)
	if err != nil {
		return nil, curriculum.EvalSuiteVersion{}, err
	}
	item, err := store.CreateEvalSuiteVersion(ctx, curriculum.CreateEvalSuiteVersionInput{OrganizationID: organizationID, ActorID: actorID, CorrelationID: strings.TrimSpace(input.CorrelationID), Name: input.Name, Version: input.Version, ParentVersion: input.ParentVersion, DevelopmentProposalIDs: input.DevelopmentProposalIDs, HeldOutProposalIDs: input.HeldOutProposalIDs})
	if err != nil {
		return nil, curriculum.EvalSuiteVersion{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "curriculum_eval_suite_version_created", map[string]any{"actor_type": "agent_or_human", "actor_id": actorID, "suite_version_id": item.ID, "suite_name": item.Name, "suite_version": item.Version, "parent_version": item.ParentVersion, "suite_revision": item.SuiteRevision, "development_case_count": len(item.DevelopmentProposalIDs), "held_out_case_count": len(item.HeldOutProposalIDs), "request_id": item.CorrelationID}); err != nil {
		return nil, curriculum.EvalSuiteVersion{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Created a new immutable eval-suite version from explicitly accepted proposal versions. No existing suite or historical experiment binding was changed."}}}, item, nil
}

func (s *Server) getCurriculumEvalSuiteVersionTool(ctx context.Context, _ *mcp.CallToolRequest, input curriculumEvalSuiteIDInput) (*mcp.CallToolResult, curriculum.EvalSuiteVersion, error) {
	organizationID, _ := s.proposalActor(ctx)
	if err := s.authorize(ctx, authz.ActionCurriculumRead, authz.Resource{OrganizationID: organizationID, ID: strings.TrimSpace(input.SuiteVersionID)}); err != nil {
		return nil, curriculum.EvalSuiteVersion{}, err
	}
	store, err := s.curriculumStore(ctx)
	if err != nil {
		return nil, curriculum.EvalSuiteVersion{}, err
	}
	item, err := store.GetEvalSuiteVersion(ctx, organizationID, strings.TrimSpace(input.SuiteVersionID))
	if err != nil {
		return nil, curriculum.EvalSuiteVersion{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned immutable eval-suite version evidence."}}}, item, nil
}

func (s *Server) prepareCurriculumHandoffTool(ctx context.Context, _ *mcp.CallToolRequest, input prepareCurriculumHandoffInput) (*mcp.CallToolResult, curriculum.CandidateHandoff, error) {
	organizationID, actorID := s.proposalActor(ctx)
	store, err := s.curriculumStore(ctx)
	if err != nil {
		return nil, curriculum.CandidateHandoff{}, err
	}
	gap, err := store.GetGap(ctx, organizationID, strings.TrimSpace(input.GapID))
	if err != nil {
		return nil, curriculum.CandidateHandoff{}, err
	}
	if err := s.authorizeCurriculumGap(ctx, organizationID, gap, authz.ActionCurriculumPrepareHandoff); err != nil {
		return nil, curriculum.CandidateHandoff{}, err
	}
	item, err := store.PrepareCandidateHandoff(ctx, curriculum.PrepareHandoffInput{OrganizationID: organizationID, GapID: gap.ID, ProposalIDs: input.ProposalIDs})
	if err != nil {
		return nil, curriculum.CandidateHandoff{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "curriculum_handoff_prepared", map[string]any{"actor_type": "agent_or_human", "actor_id": actorID, "capability_gap_id": gap.ID, "handoff_id": item.ID, "included_proposal_count": len(item.Proposals), "excluded_held_out_count": item.ExcludedHeldOutCount, "request_id": strings.TrimSpace(input.CorrelationID)}); err != nil {
		return nil, curriculum.CandidateHandoff{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Prepared candidate-generation handoff with %d accepted development proposal(s); %d held-out proposal(s) were omitted from content.", len(item.Proposals), item.ExcludedHeldOutCount)}}}, item, nil
}

func (s *Server) authorizeCurriculumGap(ctx context.Context, organizationID string, gap curriculum.CapabilityGap, action authz.Action) error {
	info, err := s.catalogue.Revision(ctx, organizationID, gap.Scope.RevisionID)
	if err != nil {
		return err
	}
	if info.SkillID != gap.Scope.CapabilityID {
		return fmt.Errorf("capability gap revision binding is invalid")
	}
	return s.authorize(ctx, action, s.capabilityAuthorizationResource(organizationID, info))
}

func (s *Server) curriculumStore(ctx context.Context) (*curriculum.Store, error) {
	if existing, ok := curriculumStores.Load(s); ok {
		store, _ := existing.(*curriculum.Store)
		if store != nil {
			return store, nil
		}
	}
	if s == nil || s.catalogue == nil {
		return nil, fmt.Errorf("curriculum persistence is unavailable")
	}
	store, err := curriculum.New(ctx, s.catalogue)
	if err != nil {
		return nil, err
	}
	actual, _ := curriculumStores.LoadOrStore(s, store)
	resolved, _ := actual.(*curriculum.Store)
	if resolved == nil {
		return nil, fmt.Errorf("curriculum persistence is unavailable")
	}
	return resolved, nil
}
