package httpserver

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/fitness"
	"github.com/mhingston/skillet/internal/improver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const improverMetaEvalEnv = "SKILLET_IMPROVER_META_EVAL"

var improverStores sync.Map // map[*Server]*improver.Store

type recordImproverProvenanceInput struct {
	ExperimentID             string                     `json:"experiment_id" jsonschema:"Exact immutable improvement experiment identifier"`
	Agent                    improver.VersionedIdentity `json:"agent,omitempty" jsonschema:"Agent identity and version evidence; each field is known, missing, or redacted"`
	Harness                  improver.VersionedIdentity `json:"harness,omitempty" jsonschema:"Harness identity and version evidence; each field is known, missing, or redacted"`
	Model                    improver.ModelIdentity     `json:"model,omitempty" jsonschema:"Provider, model, and model-revision evidence; each field is known, missing, or redacted"`
	SystemRevision           improver.EvidenceValue     `json:"system_revision,omitempty" jsonschema:"System instruction/config revision evidence"`
	PromptRevision           improver.EvidenceValue     `json:"prompt_revision,omitempty" jsonschema:"Prompt revision evidence"`
	SkillBundleRevision      improver.EvidenceValue     `json:"skill_bundle_revision,omitempty" jsonschema:"Skill bundle revision evidence"`
	ToolAdapters             improver.ComponentSet      `json:"tool_adapters,omitempty" jsonschema:"Tool/adaptor component versions or explicit missing/redacted state"`
	StrategyID               improver.EvidenceValue     `json:"strategy_id,omitempty" jsonschema:"Candidate-generation strategy identity evidence"`
	StrategyConfigState      string                     `json:"strategy_config_state,omitempty" jsonschema:"known, missing, or redacted; known configs are canonicalized and stored only as SHA-256"`
	StrategyConfigJSON       string                     `json:"strategy_config_json,omitempty" jsonschema:"Bounded JSON strategy config used only to derive a deterministic digest; raw config is not persisted"`
	ParentStrategyID         improver.EvidenceValue     `json:"parent_strategy_id,omitempty" jsonschema:"Parent improver strategy evidence when this strategy was itself derived"`
	ContextPolicyRevision    improver.EvidenceValue     `json:"context_policy_revision,omitempty" jsonschema:"Context policy revision evidence"`
	CurriculumPolicyRevision improver.EvidenceValue     `json:"curriculum_policy_revision,omitempty" jsonschema:"Curriculum policy revision evidence"`
	CorrelationID            string                     `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type improverProvenanceInput struct {
	ExperimentID string `json:"experiment_id" jsonschema:"Exact immutable improvement experiment identifier"`
}

type createImproverMetaEvalInput struct {
	Name              string              `json:"name" jsonschema:"Explicit protected meta-eval distribution name"`
	Version           string              `json:"version" jsonschema:"Explicit immutable meta-eval version"`
	DevelopmentScopes []fitness.EvalScope `json:"development_scopes" jsonschema:"Exact development eval-suite/task-distribution identities and versions"`
	HeldOutScopes     []fitness.EvalScope `json:"held_out_scopes" jsonschema:"Exact protected held-out eval-suite/task-distribution identities and versions"`
	UsefulMetric      improver.MetricSelector `json:"useful_metric" jsonschema:"Named metric whose direction defines useful improvement within this meta-eval only"`
	Budget            improver.BudgetGuard    `json:"budget,omitempty" jsonschema:"Optional named cost/runtime budget guard; metrics remain separate"`
	CorrelationID     string                  `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type improverMetaEvalIDInput struct {
	DefinitionID string `json:"definition_id" jsonschema:"Immutable protected improver meta-eval definition identifier"`
}

type evaluateImproverStrategiesInput struct {
	DefinitionID  string                           `json:"definition_id" jsonschema:"Immutable protected meta-eval definition identifier"`
	Samples       []improver.EvaluationSampleInput `json:"samples" jsonschema:"Bounded experiment samples with development/held-out fitness comparisons and optional lineage decisions"`
	CorrelationID string                           `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type improverMetaEvaluationIDInput struct {
	EvaluationID string `json:"evaluation_id" jsonschema:"Immutable deterministic improver meta-evaluation identifier"`
}

func addImproverTools(server *mcp.Server, app *Server) {
	if server == nil || app == nil || app.catalogue == nil || !improverMetaEvalEnabled() {
		return
	}
	mcp.AddTool(server, &mcp.Tool{Name: "record_improver_provenance", Description: "Record immutable evidence about the agent/harness/model/prompt/tools and candidate-generation strategy that produced one experiment candidate. Missing/redacted fields remain explicit and raw strategy config is never persisted."}, app.recordImproverProvenanceTool)
	mcp.AddTool(server, &mcp.Tool{Name: "get_improver_provenance", Description: "Read improver provenance for one experiment. If none was captured, returns explicit missing provenance rather than guessing values."}, app.getImproverProvenanceTool)
	mcp.AddTool(server, &mcp.Tool{Name: "create_improver_meta_eval", Description: "Create an immutable protected meta-eval definition with exact development and held-out task/eval distributions, one named useful metric, and optional bounded cost/runtime guards."}, app.createImproverMetaEvalTool)
	mcp.AddTool(server, &mcp.Tool{Name: "get_improver_meta_eval", Description: "Read one immutable protected improver meta-eval definition."}, app.getImproverMetaEvalTool)
	mcp.AddTool(server, &mcp.Tool{Name: "evaluate_improver_strategies", Description: "Aggregate comparable experiment, lineage, and scoped-fitness evidence by exact improver strategy/config under one protected meta-eval. Produces scoped evidence only, never a universal improver score or automatic promotion."}, app.evaluateImproverStrategiesTool)
	mcp.AddTool(server, &mcp.Tool{Name: "get_improver_meta_evaluation", Description: "Read one immutable strategy meta-evaluation with acceptance/failure, protected gain, cost/runtime, bounded-budget, and held-out evidence views."}, app.getImproverMetaEvaluationTool)
}

func improverMetaEvalEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(improverMetaEvalEnv)), "true")
}

func (s *Server) recordImproverProvenanceTool(ctx context.Context, _ *mcp.CallToolRequest, input recordImproverProvenanceInput) (*mcp.CallToolResult, improver.Provenance, error) {
	organizationID, actorID := s.proposalActor(ctx)
	experimentID := strings.TrimSpace(input.ExperimentID)
	resource, err := s.experimentResource(ctx, organizationID, experimentID)
	if err != nil {
		return nil, improver.Provenance{}, err
	}
	if err := s.authorize(ctx, authz.ActionImproverRecordProvenance, resource); err != nil {
		return nil, improver.Provenance{}, err
	}
	store, err := s.improverStore(ctx)
	if err != nil {
		return nil, improver.Provenance{}, err
	}
	item, err := store.CaptureProvenance(ctx, improver.CaptureProvenanceInput{
		OrganizationID: organizationID, ActorID: actorID, CorrelationID: strings.TrimSpace(input.CorrelationID), ExperimentID: experimentID,
		Agent: input.Agent, Harness: input.Harness, Model: input.Model, SystemRevision: input.SystemRevision,
		PromptRevision: input.PromptRevision, SkillBundleRevision: input.SkillBundleRevision, ToolAdapters: input.ToolAdapters,
		StrategyID: input.StrategyID, StrategyConfigState: input.StrategyConfigState, StrategyConfigJSON: input.StrategyConfigJSON,
		ParentStrategyID: input.ParentStrategyID, ContextPolicyRevision: input.ContextPolicyRevision, CurriculumPolicyRevision: input.CurriculumPolicyRevision,
	})
	if err != nil {
		return nil, improver.Provenance{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "improver_provenance_recorded", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "experiment_id": item.ExperimentID,
		"candidate_id": item.CandidateID, "improver_provenance_id": item.ID,
		"strategy_identity_state": item.Strategy.ID.State, "strategy_config_state": item.Strategy.ConfigSHA256.State,
		"request_id": item.CorrelationID,
	}); err != nil {
		return nil, improver.Provenance{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Recorded immutable improver provenance as evidence. Provenance text is never interpreted as policy or executable instructions."}}}, item, nil
}

func (s *Server) getImproverProvenanceTool(ctx context.Context, _ *mcp.CallToolRequest, input improverProvenanceInput) (*mcp.CallToolResult, improver.Provenance, error) {
	organizationID, _ := s.proposalActor(ctx)
	experimentID := strings.TrimSpace(input.ExperimentID)
	resource, err := s.experimentResource(ctx, organizationID, experimentID)
	if err != nil {
		return nil, improver.Provenance{}, err
	}
	if err := s.authorize(ctx, authz.ActionImproverRead, resource); err != nil {
		return nil, improver.Provenance{}, err
	}
	store, err := s.improverStore(ctx)
	if err != nil {
		return nil, improver.Provenance{}, err
	}
	item, err := store.ProvenanceOrMissing(ctx, organizationID, experimentID)
	if err != nil {
		return nil, improver.Provenance{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned bounded improver provenance evidence; uncaptured values are explicit missing evidence rather than inferred metadata."}}}, item, nil
}

func (s *Server) createImproverMetaEvalTool(ctx context.Context, _ *mcp.CallToolRequest, input createImproverMetaEvalInput) (*mcp.CallToolResult, improver.MetaEvalDefinition, error) {
	organizationID, actorID := s.proposalActor(ctx)
	resource := authz.Resource{OrganizationID: organizationID, ID: "improver-meta-eval"}
	if err := s.authorize(ctx, authz.ActionImproverConfigureMetaEval, resource); err != nil {
		return nil, improver.MetaEvalDefinition{}, err
	}
	store, err := s.improverStore(ctx)
	if err != nil {
		return nil, improver.MetaEvalDefinition{}, err
	}
	item, err := store.CreateMetaEvalDefinition(ctx, improver.CreateMetaEvalDefinitionInput{
		OrganizationID: organizationID, ActorID: actorID, CorrelationID: strings.TrimSpace(input.CorrelationID),
		Name: input.Name, Version: input.Version, DevelopmentScopes: input.DevelopmentScopes, HeldOutScopes: input.HeldOutScopes,
		UsefulMetric: input.UsefulMetric, Budget: input.Budget,
	})
	if err != nil {
		return nil, improver.MetaEvalDefinition{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "improver_meta_eval_created", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "meta_eval_id": item.ID,
		"definition_revision": item.DefinitionRevision, "meta_eval_name": item.Name, "meta_eval_version": item.Version,
		"request_id": item.CorrelationID,
	}); err != nil {
		return nil, improver.MetaEvalDefinition{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Created immutable protected improver meta-eval inputs. Experiment results cannot edit this definition."}}}, item, nil
}

func (s *Server) getImproverMetaEvalTool(ctx context.Context, _ *mcp.CallToolRequest, input improverMetaEvalIDInput) (*mcp.CallToolResult, improver.MetaEvalDefinition, error) {
	organizationID, _ := s.proposalActor(ctx)
	if err := s.authorize(ctx, authz.ActionImproverRead, authz.Resource{OrganizationID: organizationID, ID: strings.TrimSpace(input.DefinitionID)}); err != nil {
		return nil, improver.MetaEvalDefinition{}, err
	}
	store, err := s.improverStore(ctx)
	if err != nil {
		return nil, improver.MetaEvalDefinition{}, err
	}
	item, err := store.GetMetaEvalDefinition(ctx, organizationID, strings.TrimSpace(input.DefinitionID))
	if err != nil {
		return nil, improver.MetaEvalDefinition{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned immutable protected improver meta-eval definition."}}}, item, nil
}

func (s *Server) evaluateImproverStrategiesTool(ctx context.Context, _ *mcp.CallToolRequest, input evaluateImproverStrategiesInput) (*mcp.CallToolResult, improver.MetaEvaluation, error) {
	organizationID, actorID := s.proposalActor(ctx)
	if err := s.authorize(ctx, authz.ActionImproverEvaluate, authz.Resource{OrganizationID: organizationID, ID: strings.TrimSpace(input.DefinitionID)}); err != nil {
		return nil, improver.MetaEvaluation{}, err
	}
	for _, sample := range input.Samples {
		resource, err := s.experimentResource(ctx, organizationID, strings.TrimSpace(sample.ExperimentID))
		if err != nil {
			return nil, improver.MetaEvaluation{}, err
		}
		if err := s.authorize(ctx, authz.ActionImproverEvaluate, resource); err != nil {
			return nil, improver.MetaEvaluation{}, err
		}
	}
	store, err := s.improverStore(ctx)
	if err != nil {
		return nil, improver.MetaEvaluation{}, err
	}
	item, err := store.Evaluate(ctx, improver.EvaluateInput{
		OrganizationID: organizationID, ActorID: actorID, CorrelationID: strings.TrimSpace(input.CorrelationID),
		DefinitionID: strings.TrimSpace(input.DefinitionID), Samples: input.Samples,
	})
	if err != nil {
		return nil, improver.MetaEvaluation{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "improver_meta_evaluation_recorded", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "meta_evaluation_id": item.ID,
		"meta_eval_id": item.DefinitionID, "definition_revision": item.DefinitionRevision,
		"sample_count": len(item.Samples), "strategy_count": len(item.Strategies), "request_id": item.CorrelationID,
	}); err != nil {
		return nil, improver.MetaEvaluation{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Recorded protected strategy meta-evaluation evidence. No strategy was globally ranked or allowed to alter evaluators, promotion policy, canonical source, active revisions, or retrieval ranking."}}}, item, nil
}

func (s *Server) getImproverMetaEvaluationTool(ctx context.Context, _ *mcp.CallToolRequest, input improverMetaEvaluationIDInput) (*mcp.CallToolResult, improver.MetaEvaluation, error) {
	organizationID, _ := s.proposalActor(ctx)
	if err := s.authorize(ctx, authz.ActionImproverRead, authz.Resource{OrganizationID: organizationID, ID: strings.TrimSpace(input.EvaluationID)}); err != nil {
		return nil, improver.MetaEvaluation{}, err
	}
	store, err := s.improverStore(ctx)
	if err != nil {
		return nil, improver.MetaEvaluation{}, err
	}
	item, err := store.GetMetaEvaluation(ctx, organizationID, strings.TrimSpace(input.EvaluationID))
	if err != nil {
		return nil, improver.MetaEvaluation{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Returned scoped meta-evaluation across %d strategy group(s); no universal improver score is defined.", len(item.Strategies))}}}, item, nil
}

func (s *Server) improverStore(ctx context.Context) (*improver.Store, error) {
	if existing, ok := improverStores.Load(s); ok {
		store, _ := existing.(*improver.Store)
		if store != nil {
			return store, nil
		}
	}
	if s == nil || s.catalogue == nil {
		return nil, fmt.Errorf("improver persistence is unavailable")
	}
	store, err := improver.New(ctx, s.catalogue)
	if err != nil {
		return nil, err
	}
	actual, _ := improverStores.LoadOrStore(s, store)
	resolved, _ := actual.(*improver.Store)
	if resolved == nil {
		return nil, fmt.Errorf("improver persistence is unavailable")
	}
	return resolved, nil
}
