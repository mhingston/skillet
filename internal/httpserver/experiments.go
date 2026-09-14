package httpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/experiment"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const improvementExperimentsEnv = "SKILLET_IMPROVEMENT_EXPERIMENTS"

var experimentStores sync.Map // map[*Server]*experiment.Store

type createImprovementExperimentInput struct {
	ProposalID      string                      `json:"proposal_id" jsonschema:"Ready-for-review M3 proposal identifier"`
	Hypothesis      string                      `json:"hypothesis" jsonschema:"Bounded falsifiable experiment hypothesis"`
	IntendedOutcome string                      `json:"intended_outcome,omitempty" jsonschema:"Bounded measurable intended outcome; defaults to the proposal outcome"`
	Executor        experiment.ExecutorIdentity `json:"executor,omitempty" jsonschema:"Agent/model/harness/toolchain identities when known"`
	EvalSuite       experiment.EvalSuite        `json:"eval_suite" jsonschema:"Versioned protected evaluation suite and immutable thresholds"`
	Budget          experiment.Budget           `json:"budget,omitempty" jsonschema:"Declared cost/runtime/token budget metadata; Skillet does not enforce execution"`
	CorrelationID   string                      `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type improvementExperimentIDInput struct {
	ExperimentID string `json:"experiment_id" jsonschema:"Skillet-owned improvement experiment identifier"`
}

type listImprovementExperimentsInput struct {
	RevisionID string `json:"revision_id" jsonschema:"Exact immutable base revision identifier"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum experiments to return (1-100; default 25)"`
}

type submitImprovementExperimentResultInput struct {
	ExperimentID     string                         `json:"experiment_id" jsonschema:"Skillet-owned improvement experiment identifier"`
	SpecRevision     string                         `json:"spec_revision" jsonschema:"Exact content-addressed experiment spec revision"`
	HandoffSHA256    string                         `json:"handoff_sha256" jsonschema:"Exact SHA-256 digest of the canonical handoff JSON"`
	EvalSuiteID      string                         `json:"eval_suite_id" jsonschema:"Protected evaluation suite identifier from the issued spec"`
	EvalSuiteVersion string                         `json:"eval_suite_version" jsonschema:"Protected evaluation suite version from the issued spec"`
	Measurements     []experiment.Measurement       `json:"measurements,omitempty" jsonschema:"One raw measurement per protected eval; Skillet derives pass/fail against immutable thresholds"`
	Artifacts        []experiment.ArtifactReference `json:"artifacts,omitempty" jsonschema:"Bounded credential-free result artifact/evidence references"`
	FailureReason    string                         `json:"failure_reason,omitempty" jsonschema:"Bounded runner failure reason; when set, protected measurements must be omitted"`
	Summary          string                         `json:"summary,omitempty" jsonschema:"Bounded result summary"`
	CorrelationID    string                         `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

func addExperimentTools(server *mcp.Server, app *Server) {
	if server == nil || app == nil || app.catalogue == nil || !improvementExperimentsEnabled() {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_improvement_experiment",
		Description: "Issue an immutable, content-addressed experiment handoff from one ready-for-review M3 proposal. Skillet records the spec but never starts a worker or executes code.",
	}, app.createImprovementExperimentTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_improvement_experiment",
		Description: "Read one authorised improvement experiment, its exact canonical handoff, status, and bound result evidence.",
	}, app.getImprovementExperimentTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_improvement_experiments",
		Description: "List authorised improvement experiments for one exact immutable capability revision.",
	}, app.listImprovementExperimentsTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "submit_improvement_experiment_result",
		Description: "Attach result evidence only when experiment/spec/handoff/eval-suite bindings match exactly. Protected thresholds are evaluated by Skillet and cannot be supplied or weakened by result intake.",
	}, app.submitImprovementExperimentResultTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cancel_improvement_experiment",
		Description: "Cancel an issued experiment record. This records control-plane state only and cannot stop or control an external runner.",
	}, app.cancelImprovementExperimentTool)
}

func improvementExperimentsEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(improvementExperimentsEnv)), "true")
}

func (s *Server) createImprovementExperimentTool(ctx context.Context, _ *mcp.CallToolRequest, input createImprovementExperimentInput) (*mcp.CallToolResult, experiment.Experiment, error) {
	input.ProposalID = strings.TrimSpace(input.ProposalID)
	if input.ProposalID == "" {
		return nil, experiment.Experiment{}, fmt.Errorf("proposal_id is required")
	}
	organizationID, actorID := s.proposalActor(ctx)
	resource, err := s.proposalResource(ctx, organizationID, input.ProposalID)
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	if err := s.authorize(ctx, authz.ActionExperimentCreate, resource); err != nil {
		return nil, experiment.Experiment{}, err
	}
	if err := s.authorize(ctx, authz.ActionProposalRead, resource); err != nil {
		return nil, experiment.Experiment{}, err
	}
	proposals, err := s.proposalStore(ctx)
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	origin, err := proposals.Get(ctx, organizationID, input.ProposalID)
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	store, err := s.experimentStore(ctx)
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	item, err := store.Create(ctx, experiment.CreateInput{
		OrganizationID:  organizationID,
		ActorID:         actorID,
		CorrelationID:   strings.TrimSpace(input.CorrelationID),
		Proposal:        origin,
		Hypothesis:      input.Hypothesis,
		IntendedOutcome: input.IntendedOutcome,
		Executor:        input.Executor,
		EvalSuite:       input.EvalSuite,
		Budget:          input.Budget,
	})
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "improvement_experiment_issued", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "skill_id": item.CapabilityID,
		"revision_id": item.Base.RevisionID, "proposal_id": item.Origin.ProposalID, "experiment_id": item.ID,
		"spec_revision": item.SpecRevision, "handoff_sha256": item.HandoffSHA256, "request_id": item.CorrelationID,
	}); err != nil {
		return nil, experiment.Experiment{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Issued immutable experiment handoff for an external runner. Skillet did not execute code, start a worker, or change canonical capability state."}}}, item, nil
}

func (s *Server) getImprovementExperimentTool(ctx context.Context, _ *mcp.CallToolRequest, input improvementExperimentIDInput) (*mcp.CallToolResult, experiment.Experiment, error) {
	organizationID, _ := s.proposalActor(ctx)
	resource, err := s.experimentResource(ctx, organizationID, strings.TrimSpace(input.ExperimentID))
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	if err := s.authorize(ctx, authz.ActionExperimentRead, resource); err != nil {
		return nil, experiment.Experiment{}, err
	}
	store, err := s.experimentStore(ctx)
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	item, err := store.Get(ctx, organizationID, strings.TrimSpace(input.ExperimentID))
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned immutable experiment control-plane evidence; status has no effect on canonical source, governance, or ranking."}}}, item, nil
}

func (s *Server) listImprovementExperimentsTool(ctx context.Context, _ *mcp.CallToolRequest, input listImprovementExperimentsInput) (*mcp.CallToolResult, []experiment.Experiment, error) {
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
	if err := s.authorize(ctx, authz.ActionExperimentRead, resource); err != nil {
		return nil, nil, err
	}
	store, err := s.experimentStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	items, err := store.ListRevision(ctx, organizationID, input.RevisionID, limit)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Returned %d experiment record(s) for the exact immutable revision.", len(items))}}}, items, nil
}

func (s *Server) submitImprovementExperimentResultTool(ctx context.Context, _ *mcp.CallToolRequest, input submitImprovementExperimentResultInput) (*mcp.CallToolResult, experiment.Experiment, error) {
	organizationID, actorID := s.proposalActor(ctx)
	resource, err := s.experimentResource(ctx, organizationID, strings.TrimSpace(input.ExperimentID))
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	if err := s.authorize(ctx, authz.ActionExperimentSubmitResult, resource); err != nil {
		return nil, experiment.Experiment{}, err
	}
	store, err := s.experimentStore(ctx)
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	item, err := store.SubmitResult(ctx, experiment.SubmitResultInput{
		OrganizationID:   organizationID,
		ActorID:          actorID,
		CorrelationID:    strings.TrimSpace(input.CorrelationID),
		ExperimentID:     strings.TrimSpace(input.ExperimentID),
		SpecRevision:     strings.TrimSpace(input.SpecRevision),
		HandoffSHA256:    strings.TrimSpace(input.HandoffSHA256),
		EvalSuiteID:      strings.TrimSpace(input.EvalSuiteID),
		EvalSuiteVersion: strings.TrimSpace(input.EvalSuiteVersion),
		Measurements:     input.Measurements,
		Artifacts:        input.Artifacts,
		FailureReason:    input.FailureReason,
		Summary:          input.Summary,
	})
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "improvement_experiment_result_recorded", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "skill_id": item.CapabilityID,
		"revision_id": item.Base.RevisionID, "proposal_id": item.Origin.ProposalID, "experiment_id": item.ID,
		"spec_revision": item.SpecRevision, "result_sha256": item.ResultSHA256, "status": item.Status,
	}); err != nil {
		return nil, experiment.Experiment{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Recorded result evidence against the exact issued experiment. Protected thresholds were evaluated from the immutable spec; no source, ranking, or governance state changed."}}}, item, nil
}

func (s *Server) cancelImprovementExperimentTool(ctx context.Context, _ *mcp.CallToolRequest, input improvementExperimentIDInput) (*mcp.CallToolResult, experiment.Experiment, error) {
	organizationID, actorID := s.proposalActor(ctx)
	resource, err := s.experimentResource(ctx, organizationID, strings.TrimSpace(input.ExperimentID))
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	if err := s.authorize(ctx, authz.ActionExperimentCancel, resource); err != nil {
		return nil, experiment.Experiment{}, err
	}
	store, err := s.experimentStore(ctx)
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	item, err := store.Cancel(ctx, organizationID, strings.TrimSpace(input.ExperimentID), actorID)
	if err != nil {
		return nil, experiment.Experiment{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "improvement_experiment_cancelled", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "skill_id": item.CapabilityID,
		"revision_id": item.Base.RevisionID, "proposal_id": item.Origin.ProposalID, "experiment_id": item.ID,
		"spec_revision": item.SpecRevision, "status": item.Status,
	}); err != nil {
		return nil, experiment.Experiment{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Cancelled the Skillet experiment record. External execution remains outside Skillet and is not controlled by this action."}}}, item, nil
}

func (s *Server) experimentStore(ctx context.Context) (*experiment.Store, error) {
	if existing, ok := experimentStores.Load(s); ok {
		store, _ := existing.(*experiment.Store)
		if store != nil {
			return store, nil
		}
	}
	if s == nil || s.catalogue == nil {
		return nil, fmt.Errorf("experiment persistence is unavailable")
	}
	store, err := experiment.New(ctx, s.catalogue)
	if err != nil {
		return nil, err
	}
	actual, _ := experimentStores.LoadOrStore(s, store)
	resolved, _ := actual.(*experiment.Store)
	if resolved == nil {
		return nil, fmt.Errorf("experiment persistence is unavailable")
	}
	return resolved, nil
}

func (s *Server) experimentResource(ctx context.Context, organizationID, experimentID string) (authz.Resource, error) {
	if strings.TrimSpace(experimentID) == "" || s.catalogue == nil || s.catalogue.DB == nil {
		return authz.Resource{}, fmt.Errorf("experiment id is required")
	}
	var capabilityID, revisionID string
	if err := s.catalogue.DB.QueryRowContext(ctx, `SELECT capability_id, base_revision_id FROM improvement_experiments WHERE organization_id=? AND id=?`, organizationID, experimentID).Scan(&capabilityID, &revisionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authz.Resource{}, fmt.Errorf("experiment not found")
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
