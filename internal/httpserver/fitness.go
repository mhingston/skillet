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
	"github.com/mhingston/skillet/internal/fitness"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const improvementFitnessEnv = "SKILLET_IMPROVEMENT_FITNESS"

var fitnessStores sync.Map // map[*Server]*fitness.Store

type recordFitnessEvidenceInput struct {
	RevisionID    string                        `json:"revision_id" jsonschema:"Exact immutable capability revision evaluated by this run"`
	Scope         fitness.EvalScope             `json:"scope" jsonschema:"Exact eval-suite and task-distribution identity/version"`
	Executor      fitness.ExecutionIdentity     `json:"executor" jsonschema:"Exact model/harness/toolchain identity used for this evidence"`
	Run           fitness.RunMetadata           `json:"run" jsonschema:"Run, seed, sample count, and content-addressed sample-set metadata"`
	Metrics       []fitness.Metric               `json:"metrics" jsonschema:"Named quality/regression/cost/runtime/resource metrics; no global fitness score"`
	ExperimentIDs []string                       `json:"experiment_ids,omitempty" jsonschema:"Exact M4.1 experiment identifiers that contributed to this evidence"`
	Provenance    []fitness.ProvenanceReference  `json:"provenance,omitempty" jsonschema:"Bounded immutable external evidence/provenance references"`
	CorrelationID string                         `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type fitnessEvidenceIDInput struct {
	EvidenceID string `json:"evidence_id" jsonschema:"Immutable scoped fitness evidence identifier"`
}

type listRevisionFitnessEvidenceInput struct {
	RevisionID string `json:"revision_id" jsonschema:"Exact immutable capability revision identifier"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum evidence records to return (1-100; default 25)"`
}

type createFitnessPromotionPolicyInput struct {
	ChampionRevisionID string              `json:"champion_revision_id" jsonschema:"Exact immutable configured champion revision"`
	Scope              fitness.EvalScope   `json:"scope" jsonschema:"Exact eval-suite and task-distribution identity/version required by this gate"`
	Criteria           []fitness.Criterion `json:"criteria" jsonschema:"Immutable named AND-gate criteria; each criterion independently passes, fails, or is inconclusive"`
	CorrelationID      string              `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type fitnessPolicyIDInput struct {
	PolicyID string `json:"policy_id" jsonschema:"Immutable content-addressed fitness promotion policy identifier"`
}

type compareFitnessEvidenceInput struct {
	PolicyID             string `json:"policy_id" jsonschema:"Immutable configured champion and promotion criteria"`
	ChampionEvidenceID   string `json:"champion_evidence_id" jsonschema:"Scoped evidence for the exact configured champion revision"`
	ChallengerEvidenceID string `json:"challenger_evidence_id" jsonschema:"Scoped evidence for the challenger revision under the exact same eval/task distribution"`
	CorrelationID        string `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type fitnessComparisonIDInput struct {
	ComparisonID string `json:"comparison_id" jsonschema:"Immutable deterministic fitness comparison identifier"`
}

func addFitnessTools(server *mcp.Server, app *Server) {
	if server == nil || app == nil || app.catalogue == nil || !improvementFitnessEnabled() {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "record_fitness_evidence",
		Description: "Record immutable revision-scoped fitness evidence bound to an exact eval-suite/task-distribution version, executor identity, run/seed/sample metadata, named metrics, uncertainty, and provenance. No global fitness score is created.",
	}, app.recordFitnessEvidenceTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_fitness_evidence",
		Description: "Read one authorised immutable scoped fitness evidence record.",
	}, app.getFitnessEvidenceTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_revision_fitness_evidence",
		Description: "List bounded authorised scoped fitness evidence for one exact immutable capability revision.",
	}, app.listRevisionFitnessEvidenceTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_fitness_promotion_policy",
		Description: "Create an immutable content-addressed promotion policy bound to one configured champion revision and exact eval/task-distribution scope. Criteria are AND-gated and cannot be supplied by result intake.",
	}, app.createFitnessPromotionPolicyTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_fitness_promotion_policy",
		Description: "Read one authorised immutable fitness promotion policy and its exact champion/scope/criteria binding.",
	}, app.getFitnessPromotionPolicyTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "compare_fitness_evidence",
		Description: "Deterministically compare challenger evidence with configured champion evidence under one immutable policy. Returns passes_gate, fails_gate, or inconclusive and never promotes, activates, ranks, or mutates source.",
	}, app.compareFitnessEvidenceTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_fitness_comparison",
		Description: "Read one immutable champion/challenger comparison result and criterion-level decision evidence.",
	}, app.getFitnessComparisonTool)
}

func improvementFitnessEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(improvementFitnessEnv)), "true")
}

func (s *Server) recordFitnessEvidenceTool(ctx context.Context, _ *mcp.CallToolRequest, input recordFitnessEvidenceInput) (*mcp.CallToolResult, fitness.Evidence, error) {
	organizationID, actorID := s.proposalActor(ctx)
	revisionID := strings.TrimSpace(input.RevisionID)
	if revisionID == "" {
		return nil, fitness.Evidence{}, fmt.Errorf("revision_id is required")
	}
	info, err := s.catalogue.Revision(ctx, organizationID, revisionID)
	if err != nil {
		return nil, fitness.Evidence{}, err
	}
	resource := s.capabilityAuthorizationResource(organizationID, info)
	if err := s.authorize(ctx, authz.ActionFitnessRecord, resource); err != nil {
		return nil, fitness.Evidence{}, err
	}
	store, err := s.fitnessStore(ctx)
	if err != nil {
		return nil, fitness.Evidence{}, err
	}
	item, err := store.RecordEvidence(ctx, fitness.RecordEvidenceInput{
		OrganizationID: organizationID,
		ActorID:        actorID,
		CorrelationID:  strings.TrimSpace(input.CorrelationID),
		RevisionID:     revisionID,
		Scope:          input.Scope,
		Executor:       input.Executor,
		Run:            input.Run,
		Metrics:        input.Metrics,
		ExperimentIDs:  input.ExperimentIDs,
		Provenance:     input.Provenance,
	})
	if err != nil {
		return nil, fitness.Evidence{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "fitness_evidence_recorded", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "skill_id": item.Revision.CapabilityID,
		"revision_id": item.Revision.RevisionID, "fitness_evidence_id": item.ID,
		"eval_suite_id": item.Scope.EvalSuiteID, "eval_suite_version": item.Scope.EvalSuiteVersion,
		"task_distribution_id": item.Scope.TaskDistributionID, "task_distribution_version": item.Scope.TaskDistributionVersion,
		"run_id": item.Run.RunID, "request_id": item.CorrelationID,
	}); err != nil {
		return nil, fitness.Evidence{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Recorded immutable scoped fitness evidence. Canonical source, active revision, governance, and retrieval ranking were unchanged."}}}, item, nil
}

func (s *Server) getFitnessEvidenceTool(ctx context.Context, _ *mcp.CallToolRequest, input fitnessEvidenceIDInput) (*mcp.CallToolResult, fitness.Evidence, error) {
	organizationID, _ := s.proposalActor(ctx)
	evidenceID := strings.TrimSpace(input.EvidenceID)
	resource, err := s.fitnessEvidenceResource(ctx, organizationID, evidenceID)
	if err != nil {
		return nil, fitness.Evidence{}, err
	}
	if err := s.authorize(ctx, authz.ActionFitnessRead, resource); err != nil {
		return nil, fitness.Evidence{}, err
	}
	store, err := s.fitnessStore(ctx)
	if err != nil {
		return nil, fitness.Evidence{}, err
	}
	item, err := store.GetEvidence(ctx, organizationID, evidenceID)
	if err != nil {
		return nil, fitness.Evidence{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned immutable revision-scoped fitness evidence with separate named metrics and uncertainty."}}}, item, nil
}

func (s *Server) listRevisionFitnessEvidenceTool(ctx context.Context, _ *mcp.CallToolRequest, input listRevisionFitnessEvidenceInput) (*mcp.CallToolResult, []fitness.Evidence, error) {
	organizationID, _ := s.proposalActor(ctx)
	revisionID := strings.TrimSpace(input.RevisionID)
	if revisionID == "" {
		return nil, nil, fmt.Errorf("revision_id is required")
	}
	info, err := s.catalogue.Revision(ctx, organizationID, revisionID)
	if err != nil {
		return nil, nil, err
	}
	resource := s.capabilityAuthorizationResource(organizationID, info)
	if err := s.authorize(ctx, authz.ActionFitnessRead, resource); err != nil {
		return nil, nil, err
	}
	store, err := s.fitnessStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	items, err := store.ListEvidence(ctx, organizationID, revisionID, input.Limit)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Returned %d scoped fitness evidence record(s) for the exact revision.", len(items))}}}, items, nil
}

func (s *Server) createFitnessPromotionPolicyTool(ctx context.Context, _ *mcp.CallToolRequest, input createFitnessPromotionPolicyInput) (*mcp.CallToolResult, fitness.PromotionPolicy, error) {
	organizationID, actorID := s.proposalActor(ctx)
	championRevisionID := strings.TrimSpace(input.ChampionRevisionID)
	if championRevisionID == "" {
		return nil, fitness.PromotionPolicy{}, fmt.Errorf("champion_revision_id is required")
	}
	info, err := s.catalogue.Revision(ctx, organizationID, championRevisionID)
	if err != nil {
		return nil, fitness.PromotionPolicy{}, err
	}
	resource := s.capabilityAuthorizationResource(organizationID, info)
	if err := s.authorize(ctx, authz.ActionFitnessConfigurePolicy, resource); err != nil {
		return nil, fitness.PromotionPolicy{}, err
	}
	store, err := s.fitnessStore(ctx)
	if err != nil {
		return nil, fitness.PromotionPolicy{}, err
	}
	item, err := store.CreatePolicy(ctx, fitness.CreatePolicyInput{
		OrganizationID: organizationID, ActorID: actorID, CorrelationID: strings.TrimSpace(input.CorrelationID),
		ChampionRevisionID: championRevisionID, Scope: input.Scope, Criteria: input.Criteria,
	})
	if err != nil {
		return nil, fitness.PromotionPolicy{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "fitness_promotion_policy_created", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "skill_id": item.Champion.CapabilityID,
		"revision_id": item.Champion.RevisionID, "fitness_policy_id": item.ID, "policy_revision": item.PolicyRevision,
		"eval_suite_id": item.Scope.EvalSuiteID, "eval_suite_version": item.Scope.EvalSuiteVersion,
	}); err != nil {
		return nil, fitness.PromotionPolicy{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Created immutable champion/promotion criteria. This policy is gate configuration only and does not activate or promote a revision."}}}, item, nil
}

func (s *Server) getFitnessPromotionPolicyTool(ctx context.Context, _ *mcp.CallToolRequest, input fitnessPolicyIDInput) (*mcp.CallToolResult, fitness.PromotionPolicy, error) {
	organizationID, _ := s.proposalActor(ctx)
	policyID := strings.TrimSpace(input.PolicyID)
	resource, err := s.fitnessPolicyResource(ctx, organizationID, policyID)
	if err != nil {
		return nil, fitness.PromotionPolicy{}, err
	}
	if err := s.authorize(ctx, authz.ActionFitnessRead, resource); err != nil {
		return nil, fitness.PromotionPolicy{}, err
	}
	store, err := s.fitnessStore(ctx)
	if err != nil {
		return nil, fitness.PromotionPolicy{}, err
	}
	item, err := store.GetPolicy(ctx, organizationID, policyID)
	if err != nil {
		return nil, fitness.PromotionPolicy{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned immutable promotion policy with exact configured champion, eval/task scope, and named criteria."}}}, item, nil
}

func (s *Server) compareFitnessEvidenceTool(ctx context.Context, _ *mcp.CallToolRequest, input compareFitnessEvidenceInput) (*mcp.CallToolResult, fitness.Comparison, error) {
	organizationID, actorID := s.proposalActor(ctx)
	policyID := strings.TrimSpace(input.PolicyID)
	resource, err := s.fitnessPolicyResource(ctx, organizationID, policyID)
	if err != nil {
		return nil, fitness.Comparison{}, err
	}
	if err := s.authorize(ctx, authz.ActionFitnessCompare, resource); err != nil {
		return nil, fitness.Comparison{}, err
	}
	store, err := s.fitnessStore(ctx)
	if err != nil {
		return nil, fitness.Comparison{}, err
	}
	item, err := store.Compare(ctx, fitness.CompareInput{
		OrganizationID: organizationID, ActorID: actorID, CorrelationID: strings.TrimSpace(input.CorrelationID),
		PolicyID: policyID, ChampionEvidenceID: strings.TrimSpace(input.ChampionEvidenceID), ChallengerEvidenceID: strings.TrimSpace(input.ChallengerEvidenceID),
	})
	if err != nil {
		return nil, fitness.Comparison{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "fitness_comparison_recorded", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "skill_id": item.CapabilityID,
		"revision_id": item.ChallengerRevisionID, "fitness_comparison_id": item.ID,
		"fitness_policy_id": item.Result.PolicyID, "policy_revision": item.Result.PolicyRevision,
		"champion_revision_id": item.ChampionRevisionID, "decision": item.Result.Decision,
	}); err != nil {
		return nil, fitness.Comparison{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Comparison decision: %s. The result is evidence only; human/source review remains the promotion boundary and no canonical state changed.", item.Result.Decision)}}}, item, nil
}

func (s *Server) getFitnessComparisonTool(ctx context.Context, _ *mcp.CallToolRequest, input fitnessComparisonIDInput) (*mcp.CallToolResult, fitness.Comparison, error) {
	organizationID, _ := s.proposalActor(ctx)
	comparisonID := strings.TrimSpace(input.ComparisonID)
	resource, err := s.fitnessComparisonResource(ctx, organizationID, comparisonID)
	if err != nil {
		return nil, fitness.Comparison{}, err
	}
	if err := s.authorize(ctx, authz.ActionFitnessRead, resource); err != nil {
		return nil, fitness.Comparison{}, err
	}
	store, err := s.fitnessStore(ctx)
	if err != nil {
		return nil, fitness.Comparison{}, err
	}
	item, err := store.GetComparison(ctx, organizationID, comparisonID)
	if err != nil {
		return nil, fitness.Comparison{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned immutable criterion-level champion/challenger gate evidence. No global fitness score is present."}}}, item, nil
}

func (s *Server) fitnessStore(ctx context.Context) (*fitness.Store, error) {
	if existing, ok := fitnessStores.Load(s); ok {
		store, _ := existing.(*fitness.Store)
		if store != nil {
			return store, nil
		}
	}
	if s == nil || s.catalogue == nil {
		return nil, fmt.Errorf("fitness persistence is unavailable")
	}
	store, err := fitness.New(ctx, s.catalogue)
	if err != nil {
		return nil, err
	}
	actual, _ := fitnessStores.LoadOrStore(s, store)
	resolved, _ := actual.(*fitness.Store)
	if resolved == nil {
		return nil, fmt.Errorf("fitness persistence is unavailable")
	}
	return resolved, nil
}

func (s *Server) fitnessEvidenceResource(ctx context.Context, organizationID, evidenceID string) (authz.Resource, error) {
	if evidenceID == "" || s == nil || s.catalogue == nil || s.catalogue.DB == nil {
		return authz.Resource{}, fmt.Errorf("fitness evidence id is required")
	}
	var revisionID string
	if err := s.catalogue.DB.QueryRowContext(ctx, `SELECT revision_id FROM fitness_evidence WHERE organization_id=? AND id=?`, organizationID, evidenceID).Scan(&revisionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authz.Resource{}, fmt.Errorf("fitness evidence not found")
		}
		return authz.Resource{}, err
	}
	return s.fitnessRevisionResource(ctx, organizationID, revisionID)
}

func (s *Server) fitnessPolicyResource(ctx context.Context, organizationID, policyID string) (authz.Resource, error) {
	if policyID == "" || s == nil || s.catalogue == nil || s.catalogue.DB == nil {
		return authz.Resource{}, fmt.Errorf("fitness policy id is required")
	}
	var revisionID string
	if err := s.catalogue.DB.QueryRowContext(ctx, `SELECT champion_revision_id FROM fitness_promotion_policies WHERE organization_id=? AND id=?`, organizationID, policyID).Scan(&revisionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authz.Resource{}, fmt.Errorf("fitness policy not found")
		}
		return authz.Resource{}, err
	}
	return s.fitnessRevisionResource(ctx, organizationID, revisionID)
}

func (s *Server) fitnessComparisonResource(ctx context.Context, organizationID, comparisonID string) (authz.Resource, error) {
	if comparisonID == "" || s == nil || s.catalogue == nil || s.catalogue.DB == nil {
		return authz.Resource{}, fmt.Errorf("fitness comparison id is required")
	}
	var revisionID string
	if err := s.catalogue.DB.QueryRowContext(ctx, `SELECT champion_revision_id FROM fitness_comparisons WHERE organization_id=? AND id=?`, organizationID, comparisonID).Scan(&revisionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authz.Resource{}, fmt.Errorf("fitness comparison not found")
		}
		return authz.Resource{}, err
	}
	return s.fitnessRevisionResource(ctx, organizationID, revisionID)
}

func (s *Server) fitnessRevisionResource(ctx context.Context, organizationID, revisionID string) (authz.Resource, error) {
	info, err := s.catalogue.Revision(ctx, organizationID, revisionID)
	if err != nil {
		return authz.Resource{}, err
	}
	return s.capabilityAuthorizationResource(organizationID, info), nil
}
