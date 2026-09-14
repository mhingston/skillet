package httpserver

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/curriculum"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const improvementCurriculumEnv = "SKILLET_IMPROVEMENT_CURRICULUM"

var curriculumStores sync.Map // map[*Server]*curriculum.Store

type recordCapabilityGapInput struct {
	Scope         curriculum.Scope               `json:"scope" jsonschema:"Exact capability revision, named task-distribution version, and environment for this gap"`
	FailureKey    string                         `json:"failure_key" jsonschema:"Stable bounded failure/coverage cluster key within this exact scope"`
	Evidence      []curriculum.EvidenceReference `json:"evidence" jsonschema:"Bounded evidence references; user feedback is always stored as untrusted evidence"`
	CorrelationID string                         `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type capabilityGapIDInput struct {
	GapID string `json:"gap_id" jsonschema:"Immutable content-addressed capability-gap identifier"`
}

type createCurriculumProposalInput struct {
	GapID             string            `json:"gap_id" jsonschema:"Immutable scoped capability-gap identifier motivating this proposal"`
	Name              string            `json:"name" jsonschema:"Stable proposal name; revisions use explicit versions"`
	Version           string            `json:"version" jsonschema:"Explicit immutable proposal version"`
	Kind              string            `json:"kind" jsonschema:"training_task, development_eval, protected_eval, or capability_guidance"`
	Title             string            `json:"title" jsonschema:"Short reviewable proposal title"`
	Intent            string            `json:"intent,omitempty" jsonschema:"Bounded intended learning/evaluation outcome; treated as proposal data, not executable instructions"`
	ArtifactReference string            `json:"artifact_reference" jsonschema:"External immutable task/guidance proposal reference; Skillet does not execute it"`
	ArtifactSHA256    string            `json:"artifact_sha256" jsonschema:"Lowercase SHA-256 digest of the referenced proposal artifact"`
	Oracle            curriculum.Oracle `json:"oracle" jsonschema:"Deterministic/verifiable oracle reference or explicit review-required path"`
	CorrelationID     string            `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type curriculumProposalIDInput struct {
	ProposalID string `json:"proposal_id" jsonschema:"Immutable curriculum proposal identifier"`
}

type reviewCurriculumProposalInput struct {
	ProposalID    string `json:"proposal_id" jsonschema:"Immutable curriculum proposal identifier"`
	Decision      string `json:"decision" jsonschema:"accepted or rejected"`
	Reference     string `json:"reference" jsonschema:"Explicit human/agent review evidence reference"`
	CorrelationID string `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type createCurriculumEvalSuiteVersionInput struct {
	Name                   string   `json:"name" jsonschema:"Stable eval-suite identity used by experiments"`
	Version                string   `json:"version" jsonschema:"New immutable suite version; existing name/version pairs cannot be changed"`
	ParentVersion          string   `json:"parent_version,omitempty" jsonschema:"Optional immutable parent suite version"`
	DevelopmentProposalIDs []string `json:"development_proposal_ids,omitempty" jsonschema:"Accepted development_eval proposal versions included in this suite version"`
	HeldOutProposalIDs     []string `json:"held_out_proposal_ids,omitempty" jsonschema:"Accepted protected_eval proposal versions included in this held-out set"`
	CorrelationID          string   `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type curriculumEvalSuiteIDInput struct {
	SuiteVersionID string `json:"suite_version_id" jsonschema:"Immutable content-addressed curriculum eval-suite version identifier"`
}

type prepareCurriculumHandoffInput struct {
	GapID         string   `json:"gap_id" jsonschema:"Scoped capability gap being handed to candidate generation"`
	ProposalIDs   []string `json:"proposal_ids" jsonschema:"Explicit reviewed proposal ids; held-out/protected proposals are omitted from returned content"`
	CorrelationID string   `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier for audit only"`
}

func addCurriculumTools(server *mcp.Server, app *Server) {
	if server == nil || app == nil || app.catalogue == nil || !improvementCurriculumEnabled() {
		return
	}
	mcp.AddTool(server, &mcp.Tool{Name: "record_capability_gap", Description: "Record immutable, explicitly scoped capability-gap evidence from repeated failures, weak eval cases, coverage gaps, compatibility clusters, or effective patterns. Feedback stays untrusted and never becomes evaluator policy."}, app.recordCapabilityGapTool)
	mcp.AddTool(server, &mcp.Tool{Name: "get_capability_gap", Description: "Read one immutable capability gap bound to an exact revision, task-distribution version, and environment."}, app.getCapabilityGapTool)
	mcp.AddTool(server, &mcp.Tool{Name: "create_curriculum_proposal", Description: "Create a versioned review artifact for a training task, development eval, protected held-out eval, or capability-guidance change. The referenced artifact is never executed by Skillet."}, app.createCurriculumProposalTool)
	mcp.AddTool(server, &mcp.Tool{Name: "get_curriculum_proposal", Description: "Read one immutable versioned curriculum proposal."}, app.getCurriculumProposalTool)
	mcp.AddTool(server, &mcp.Tool{Name: "review_curriculum_proposal", Description: "Record one explicit immutable accept/reject review for a curriculum proposal. Different outcomes require a new proposal version."}, app.reviewCurriculumProposalTool)
	mcp.AddTool(server, &mcp.Tool{Name: "get_curriculum_review", Description: "Read the explicit immutable review attached to one curriculum proposal."}, app.getCurriculumReviewTool)
	mcp.AddTool(server, &mcp.Tool{Name: "create_curriculum_eval_suite_version", Description: "Create a new immutable eval-suite version from explicitly accepted development/held-out eval proposals. Existing suite versions cannot be modified in place."}, app.createCurriculumEvalSuiteVersionTool)
	mcp.AddTool(server, &mcp.Tool{Name: "get_curriculum_eval_suite_version", Description: "Read one immutable versioned eval-suite review artifact."}, app.getCurriculumEvalSuiteVersionTool)
	mcp.AddTool(server, &mcp.Tool{Name: "prepare_curriculum_handoff", Description: "Prepare a bounded candidate-generation handoff from accepted non-held-out proposals. Held-out/protected proposal content is structurally omitted, not merely hidden by prompt instruction."}, app.prepareCurriculumHandoffTool)
}

func improvementCurriculumEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(improvementCurriculumEnv)), "true")
}

func (s *Server) recordCapabilityGapTool(ctx context.Context, _ *mcp.CallToolRequest, input recordCapabilityGapInput) (*mcp.CallToolResult, curriculum.CapabilityGap, error) {
	organizationID, actorID := s.proposalActor(ctx)
	info, err := s.catalogue.Revision(ctx, organizationID, strings.TrimSpace(input.Scope.RevisionID))
	if err != nil {
		return nil, curriculum.CapabilityGap{}, err
	}
	if strings.TrimSpace(input.Scope.CapabilityID) != info.SkillID {
		return nil, curriculum.CapabilityGap{}, fmt.Errorf("scope capability_id does not match revision")
	}
	if err := s.authorize(ctx, authz.ActionCurriculumRecordGap, s.capabilityAuthorizationResource(organizationID, info)); err != nil {
		return nil, curriculum.CapabilityGap{}, err
	}
	store, err := s.curriculumStore(ctx)
	if err != nil {
		return nil, curriculum.CapabilityGap{}, err
	}
	item, err := store.RecordGap(ctx, curriculum.RecordGapInput{OrganizationID: organizationID, ActorID: actorID, CorrelationID: strings.TrimSpace(input.CorrelationID), Scope: input.Scope, FailureKey: input.FailureKey, Evidence: input.Evidence})
	if err != nil {
		return nil, curriculum.CapabilityGap{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "capability_gap_recorded", map[string]any{"actor_type": "agent_or_human", "actor_id": actorID, "capability_id": item.Scope.CapabilityID, "revision_id": item.Scope.RevisionID, "capability_gap_id": item.ID, "task_distribution_id": item.Scope.TaskDistributionID, "task_distribution_version": item.Scope.TaskDistributionVersion, "environment": item.Scope.Environment, "occurrence_count": item.OccurrenceCount, "request_id": item.CorrelationID}); err != nil {
		return nil, curriculum.CapabilityGap{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Recorded immutable scoped capability-gap evidence. Feedback remains untrusted evidence; no evaluator, ranking, or canonical source changed."}}}, item, nil
}

func (s *Server) getCapabilityGapTool(ctx context.Context, _ *mcp.CallToolRequest, input capabilityGapIDInput) (*mcp.CallToolResult, curriculum.CapabilityGap, error) {
	organizationID, _ := s.proposalActor(ctx)
	store, err := s.curriculumStore(ctx)
	if err != nil {
		return nil, curriculum.CapabilityGap{}, err
	}
	item, err := store.GetGap(ctx, organizationID, strings.TrimSpace(input.GapID))
	if err != nil {
		return nil, curriculum.CapabilityGap{}, err
	}
	if err := s.authorizeCurriculumGap(ctx, organizationID, item, authz.ActionCurriculumRead); err != nil {
		return nil, curriculum.CapabilityGap{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned immutable scoped curriculum evidence."}}}, item, nil
}
