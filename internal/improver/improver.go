// Package improver owns opt-in, append-only improver provenance and protected
// meta-evaluation evidence. It never executes candidate-generation strategies,
// changes protected evaluators, promotes revisions, mutates canonical source, or
// participates in retrieval ranking.
package improver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/experiment"
	"github.com/mhingston/skillet/internal/fitness"
	"github.com/mhingston/skillet/internal/lineage"
)

const (
	EvidenceKnown    = "known"
	EvidenceMissing  = "missing"
	EvidenceRedacted = "redacted"

	MaxIdentityBytes     = 256
	MaxStrategyConfig    = 64 * 1024
	MaxComponents        = 64
	MaxScopes            = 64
	MaxSamples           = 100
	MaxDefinitionName    = 256
	MaxDefinitionVersion = 128
)

type EvidenceValue struct {
	State string `json:"state"`
	Value string `json:"value,omitempty"`
}

type VersionedIdentity struct {
	Identity EvidenceValue `json:"identity"`
	Version  EvidenceValue `json:"version"`
}

type ModelIdentity struct {
	Provider EvidenceValue `json:"provider"`
	Model    EvidenceValue `json:"model"`
	Revision EvidenceValue `json:"revision"`
}

type ComponentVersion struct {
	Name    string        `json:"name"`
	Version EvidenceValue `json:"version"`
}

type ComponentSet struct {
	State      string             `json:"state"`
	Components []ComponentVersion `json:"components,omitempty"`
}

type StrategyIdentity struct {
	ID               EvidenceValue `json:"id"`
	ConfigSHA256     EvidenceValue `json:"config_sha256"`
	ParentStrategyID EvidenceValue `json:"parent_strategy_id"`
}

type Provenance struct {
	ID                       string            `json:"id,omitempty"`
	OrganizationID           string            `json:"organization_id"`
	ExperimentID             string            `json:"experiment_id"`
	CandidateID              string            `json:"candidate_id"`
	Agent                    VersionedIdentity `json:"agent"`
	Harness                  VersionedIdentity `json:"harness"`
	Model                    ModelIdentity     `json:"model"`
	SystemRevision           EvidenceValue     `json:"system_revision"`
	PromptRevision           EvidenceValue     `json:"prompt_revision"`
	SkillBundleRevision      EvidenceValue     `json:"skill_bundle_revision"`
	ToolAdapters             ComponentSet      `json:"tool_adapters"`
	Strategy                 StrategyIdentity  `json:"strategy"`
	ContextPolicyRevision    EvidenceValue     `json:"context_policy_revision"`
	CurriculumPolicyRevision EvidenceValue     `json:"curriculum_policy_revision"`
	ActorID                  string            `json:"actor_id,omitempty"`
	CorrelationID            string            `json:"correlation_id,omitempty"`
	CreatedAt                string            `json:"created_at,omitempty"`
}

type CaptureProvenanceInput struct {
	OrganizationID           string
	ActorID                  string
	CorrelationID            string
	ExperimentID             string
	Agent                    VersionedIdentity
	Harness                  VersionedIdentity
	Model                    ModelIdentity
	SystemRevision           EvidenceValue
	PromptRevision           EvidenceValue
	SkillBundleRevision      EvidenceValue
	ToolAdapters             ComponentSet
	StrategyID               EvidenceValue
	StrategyConfigState      string
	StrategyConfigJSON       string
	ParentStrategyID         EvidenceValue
	ContextPolicyRevision    EvidenceValue
	CurriculumPolicyRevision EvidenceValue
}

type MetricSelector struct {
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	HigherIsBetter bool   `json:"higher_is_better"`
}

type BudgetGuard struct {
	CostMetricName    string  `json:"cost_metric_name,omitempty"`
	CostUnit          string  `json:"cost_unit,omitempty"`
	MaxCost           float64 `json:"max_cost,omitempty"`
	RuntimeMetricName string  `json:"runtime_metric_name,omitempty"`
	RuntimeUnit       string  `json:"runtime_unit,omitempty"`
	MaxRuntime        float64 `json:"max_runtime,omitempty"`
}

type MetaEvalDefinition struct {
	ID                string              `json:"id"`
	OrganizationID    string              `json:"organization_id"`
	Name              string              `json:"name"`
	Version           string              `json:"version"`
	DevelopmentScopes []fitness.EvalScope `json:"development_scopes"`
	HeldOutScopes     []fitness.EvalScope `json:"held_out_scopes"`
	UsefulMetric      MetricSelector      `json:"useful_metric"`
	Budget            BudgetGuard         `json:"budget"`
	DefinitionRevision string             `json:"definition_revision"`
	ActorID           string              `json:"actor_id"`
	CorrelationID     string              `json:"correlation_id,omitempty"`
	CreatedAt         string              `json:"created_at"`
}

type CreateMetaEvalDefinitionInput struct {
	OrganizationID    string
	ActorID           string
	CorrelationID     string
	Name              string
	Version           string
	DevelopmentScopes []fitness.EvalScope
	HeldOutScopes     []fitness.EvalScope
	UsefulMetric      MetricSelector
	Budget            BudgetGuard
}

type EvaluationSampleInput struct {
	ExperimentID             string `json:"experiment_id"`
	LineageID                string `json:"lineage_id,omitempty"`
	DevelopmentComparisonID  string `json:"development_comparison_id,omitempty"`
	HeldOutComparisonID      string `json:"held_out_comparison_id,omitempty"`
}

type StrategyRef struct {
	ID               EvidenceValue `json:"id"`
	ConfigSHA256     EvidenceValue `json:"config_sha256"`
	ParentStrategyID EvidenceValue `json:"parent_strategy_id"`
	Comparable       bool          `json:"comparable"`
}

type DecisionCounts struct {
	Passes       int `json:"passes"`
	Fails        int `json:"fails"`
	Inconclusive int `json:"inconclusive"`
}

type Distribution struct {
	Count  int       `json:"count"`
	Min    float64   `json:"min,omitempty"`
	Median float64   `json:"median,omitempty"`
	Max    float64   `json:"max,omitempty"`
	Values []float64 `json:"values,omitempty"`
}

type MetricDistribution struct {
	Kind         string       `json:"kind"`
	Name         string       `json:"name"`
	Unit         string       `json:"unit"`
	Distribution Distribution `json:"distribution"`
}

type StrategySummary struct {
	Strategy                         StrategyRef          `json:"strategy"`
	SampleCount                      int                  `json:"sample_count"`
	CompletedExperiments             int                  `json:"completed_experiments"`
	FailedExperiments                int                  `json:"failed_experiments"`
	AcceptedCandidates               int                  `json:"accepted_candidates"`
	RejectedCandidates               int                  `json:"rejected_candidates"`
	UndecidedCandidates              int                  `json:"undecided_candidates"`
	AcceptanceRate                   *float64             `json:"acceptance_rate,omitempty"`
	DevelopmentDecisions             DecisionCounts       `json:"development_decisions"`
	HeldOutDecisions                 DecisionCounts       `json:"held_out_decisions"`
	ProtectedDevelopmentGain         []MetricDistribution `json:"protected_development_gain"`
	ProtectedHeldOutGain             []MetricDistribution `json:"protected_held_out_gain"`
	CostPerCompletedExperiment       []MetricDistribution `json:"cost_per_completed_experiment"`
	RuntimePerCompletedExperiment    []MetricDistribution `json:"runtime_per_completed_experiment"`
	UsefulGainWithinBudget           MetricDistribution   `json:"useful_gain_within_budget"`
	UsefulGainPerCostWithinBudget    MetricDistribution   `json:"useful_gain_per_cost_within_budget"`
	BudgetCompliantCompleted         int                  `json:"budget_compliant_completed"`
	BudgetExceededCompleted          int                  `json:"budget_exceeded_completed"`
	BudgetUnknownCompleted           int                  `json:"budget_unknown_completed"`
	DevelopmentPassHeldOutFailCount  int                  `json:"development_pass_held_out_fail_count"`
	OverfitWarning                   bool                 `json:"overfit_warning"`
}

type PairwiseView struct {
	StrategyA  StrategyRef `json:"strategy_a"`
	StrategyB  StrategyRef `json:"strategy_b"`
	Conclusion string      `json:"conclusion"`
	Reason     string      `json:"reason"`
}

type MetaEvaluation struct {
	ID                 string                  `json:"id"`
	OrganizationID     string                  `json:"organization_id"`
	DefinitionID       string                  `json:"definition_id"`
	DefinitionRevision string                  `json:"definition_revision"`
	Samples            []EvaluationSampleInput `json:"samples"`
	Strategies         []StrategySummary       `json:"strategies"`
	Pairwise           []PairwiseView          `json:"pairwise"`
	ActorID            string                  `json:"actor_id"`
	CorrelationID      string                  `json:"correlation_id,omitempty"`
	CreatedAt          string                  `json:"created_at"`
}

type EvaluateInput struct {
	OrganizationID string
	ActorID        string
	CorrelationID  string
	DefinitionID   string
	Samples        []EvaluationSampleInput
}

type Store struct {
	Catalogue   *catalogue.Store
	Experiments *experiment.Store
	Fitness     *fitness.Store
	Lineage     *lineage.Store
}

func New(ctx context.Context, catalog *catalogue.Store) (*Store, error) {
	if catalog == nil || catalog.DB == nil {
		return nil, fmt.Errorf("improver catalogue is required")
	}
	experiments, err := experiment.New(ctx, catalog)
	if err != nil {
		return nil, err
	}
	fitnessStore, err := fitness.New(ctx, catalog)
	if err != nil {
		return nil, err
	}
	lineageStore, err := lineage.New(ctx, catalog)
	if err != nil {
		return nil, err
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS improver_provenance (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			experiment_id TEXT NOT NULL REFERENCES improvement_experiments(id),
			candidate_id TEXT NOT NULL,
			provenance_json TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(organization_id, experiment_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_improver_provenance_strategy ON improver_provenance(organization_id, experiment_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS improver_meta_eval_definitions (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			name TEXT NOT NULL,
			version TEXT NOT NULL,
			definition_json TEXT NOT NULL,
			definition_revision TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(organization_id, name, version)
		)`,
		`CREATE TABLE IF NOT EXISTS improver_meta_evaluations (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			definition_id TEXT NOT NULL REFERENCES improver_meta_eval_definitions(id),
			definition_revision TEXT NOT NULL,
			samples_json TEXT NOT NULL,
			strategies_json TEXT NOT NULL,
			pairwise_json TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, statement := range statements {
		if _, err := catalog.DB.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize improver schema: %w", err)
		}
	}
	return &Store{Catalogue: catalog, Experiments: experiments, Fitness: fitnessStore, Lineage: lineageStore}, nil
}

func (s *Store) CaptureProvenance(ctx context.Context, input CaptureProvenanceInput) (Provenance, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.ExperimentID = strings.TrimSpace(input.ExperimentID)
	if input.OrganizationID == "" || input.ActorID == "" || input.ExperimentID == "" {
		return Provenance{}, fmt.Errorf("organization, actor, and experiment are required")
	}
	if err := validateText("actor", input.ActorID, MaxIdentityBytes, true); err != nil {
		return Provenance{}, err
	}
	exp, err := s.Experiments.Get(ctx, input.OrganizationID, input.ExperimentID)
	if err != nil {
		return Provenance{}, err
	}
	provenance := Provenance{
		OrganizationID:           input.OrganizationID,
		ExperimentID:             exp.ID,
		CandidateID:              exp.Origin.CandidateID,
		Agent:                    input.Agent,
		Harness:                  input.Harness,
		Model:                    input.Model,
		SystemRevision:           input.SystemRevision,
		PromptRevision:           input.PromptRevision,
		SkillBundleRevision:      input.SkillBundleRevision,
		ToolAdapters:             input.ToolAdapters,
		Strategy:                 StrategyIdentity{ID: input.StrategyID, ParentStrategyID: input.ParentStrategyID},
		ContextPolicyRevision:    input.ContextPolicyRevision,
		CurriculumPolicyRevision: input.CurriculumPolicyRevision,
		ActorID:                  input.ActorID,
		CorrelationID:            input.CorrelationID,
	}
	if err := normalizeProvenance(&provenance, input.StrategyConfigState, input.StrategyConfigJSON); err != nil {
		return Provenance{}, err
	}
	payload := provenance
	payload.ID, payload.ActorID, payload.CorrelationID, payload.CreatedAt = "", "", "", ""
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Provenance{}, err
	}
	id := contentID("impprov_", input.OrganizationID, payloadJSON)
	if existing, err := s.GetProvenance(ctx, input.OrganizationID, exp.ID); err == nil {
		if existing.ID == id {
			return existing, nil
		}
		return Provenance{}, fmt.Errorf("improver provenance is immutable once captured for an experiment")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Provenance{}, err
	}
	provenanceJSON, _ := json.Marshal(payload)
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO improver_provenance(
		id, organization_id, experiment_id, candidate_id, provenance_json, actor_id, correlation_id
	) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, input.OrganizationID, exp.ID, exp.Origin.CandidateID, string(provenanceJSON), input.ActorID, input.CorrelationID)
	if err != nil {
		return Provenance{}, err
	}
	return s.GetProvenance(ctx, input.OrganizationID, exp.ID)
}

func (s *Store) GetProvenance(ctx context.Context, organizationID, experimentID string) (Provenance, error) {
	organizationID, experimentID = strings.TrimSpace(organizationID), strings.TrimSpace(experimentID)
	if organizationID == "" || experimentID == "" {
		return Provenance{}, fmt.Errorf("organization and experiment are required")
	}
	var id, candidateID, raw, actorID, correlationID, createdAt string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id, candidate_id, provenance_json, actor_id, correlation_id, created_at
		FROM improver_provenance WHERE organization_id=? AND experiment_id=?`, organizationID, experimentID).Scan(
		&id, &candidateID, &raw, &actorID, &correlationID, &createdAt)
	if err != nil {
		return Provenance{}, err
	}
	var item Provenance
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return Provenance{}, fmt.Errorf("decode improver provenance: %w", err)
	}
	item.ID, item.OrganizationID, item.ExperimentID, item.CandidateID = id, organizationID, experimentID, candidateID
	item.ActorID, item.CorrelationID, item.CreatedAt = actorID, correlationID, createdAt
	return item, nil
}

func (s *Store) ProvenanceOrMissing(ctx context.Context, organizationID, experimentID string) (Provenance, error) {
	item, err := s.GetProvenance(ctx, organizationID, experimentID)
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Provenance{}, err
	}
	exp, err := s.Experiments.Get(ctx, organizationID, experimentID)
	if err != nil {
		return Provenance{}, err
	}
	item = Provenance{OrganizationID: organizationID, ExperimentID: exp.ID, CandidateID: exp.Origin.CandidateID}
	if err := normalizeProvenance(&item, EvidenceMissing, ""); err != nil {
		return Provenance{}, err
	}
	return item, nil
}

func (s *Store) CreateMetaEvalDefinition(ctx context.Context, input CreateMetaEvalDefinitionInput) (MetaEvalDefinition, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.Name = strings.TrimSpace(input.Name)
	input.Version = strings.TrimSpace(input.Version)
	if input.OrganizationID == "" || input.ActorID == "" || input.Name == "" || input.Version == "" {
		return MetaEvalDefinition{}, fmt.Errorf("organization, actor, name, and version are required")
	}
	if err := validateText("meta-eval name", input.Name, MaxDefinitionName, true); err != nil {
		return MetaEvalDefinition{}, err
	}
	if err := validateText("meta-eval version", input.Version, MaxDefinitionVersion, true); err != nil {
		return MetaEvalDefinition{}, err
	}
	development, err := normalizeScopes(input.DevelopmentScopes)
	if err != nil {
		return MetaEvalDefinition{}, fmt.Errorf("development scopes: %w", err)
	}
	heldOut, err := normalizeScopes(input.HeldOutScopes)
	if err != nil {
		return MetaEvalDefinition{}, fmt.Errorf("held-out scopes: %w", err)
	}
	if len(development) == 0 || len(heldOut) == 0 {
		return MetaEvalDefinition{}, fmt.Errorf("meta-eval requires explicit development and held-out scopes")
	}
	selector, err := normalizeMetricSelector(input.UsefulMetric)
	if err != nil {
		return MetaEvalDefinition{}, err
	}
	budget, err := normalizeBudget(input.Budget)
	if err != nil {
		return MetaEvalDefinition{}, err
	}
	protected := struct {
		Name              string              `json:"name"`
		Version           string              `json:"version"`
		DevelopmentScopes []fitness.EvalScope `json:"development_scopes"`
		HeldOutScopes     []fitness.EvalScope `json:"held_out_scopes"`
		UsefulMetric      MetricSelector      `json:"useful_metric"`
		Budget            BudgetGuard         `json:"budget"`
	}{input.Name, input.Version, development, heldOut, selector, budget}
	raw, err := json.Marshal(protected)
	if err != nil {
		return MetaEvalDefinition{}, err
	}
	sum := sha256.Sum256(raw)
	revision := "sha256:" + hex.EncodeToString(sum[:])
	id := contentID("impmeta_", input.OrganizationID, raw)
	if existing, err := s.getDefinitionByNameVersion(ctx, input.OrganizationID, input.Name, input.Version); err == nil {
		if existing.DefinitionRevision != revision {
			return MetaEvalDefinition{}, fmt.Errorf("meta-eval name/version already exists with different protected inputs")
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return MetaEvalDefinition{}, err
	}
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO improver_meta_eval_definitions(
		id, organization_id, name, version, definition_json, definition_revision, actor_id, correlation_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, input.OrganizationID, input.Name, input.Version, string(raw), revision, input.ActorID, input.CorrelationID)
	if err != nil {
		return MetaEvalDefinition{}, err
	}
	return s.GetMetaEvalDefinition(ctx, input.OrganizationID, id)
}

func (s *Store) GetMetaEvalDefinition(ctx context.Context, organizationID, id string) (MetaEvalDefinition, error) {
	organizationID, id = strings.TrimSpace(organizationID), strings.TrimSpace(id)
	if organizationID == "" || id == "" {
		return MetaEvalDefinition{}, fmt.Errorf("organization and meta-eval definition id are required")
	}
	var item MetaEvalDefinition
	var raw string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id, organization_id, definition_json, definition_revision, actor_id, correlation_id, created_at
		FROM improver_meta_eval_definitions WHERE organization_id=? AND id=?`, organizationID, id).Scan(
		&item.ID, &item.OrganizationID, &raw, &item.DefinitionRevision, &item.ActorID, &item.CorrelationID, &item.CreatedAt)
	if err != nil {
		return MetaEvalDefinition{}, err
	}
	var protected struct {
		Name              string              `json:"name"`
		Version           string              `json:"version"`
		DevelopmentScopes []fitness.EvalScope `json:"development_scopes"`
		HeldOutScopes     []fitness.EvalScope `json:"held_out_scopes"`
		UsefulMetric      MetricSelector      `json:"useful_metric"`
		Budget            BudgetGuard         `json:"budget"`
	}
	if err := json.Unmarshal([]byte(raw), &protected); err != nil {
		return MetaEvalDefinition{}, fmt.Errorf("decode meta-eval definition: %w", err)
	}
	item.Name, item.Version = protected.Name, protected.Version
	item.DevelopmentScopes, item.HeldOutScopes = protected.DevelopmentScopes, protected.HeldOutScopes
	item.UsefulMetric, item.Budget = protected.UsefulMetric, protected.Budget
	return item, nil
}

func (s *Store) getDefinitionByNameVersion(ctx context.Context, organizationID, name, version string) (MetaEvalDefinition, error) {
	var id string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id FROM improver_meta_eval_definitions WHERE organization_id=? AND name=? AND version=?`, organizationID, name, version).Scan(&id)
	if err != nil {
		return MetaEvalDefinition{}, err
	}
	return s.GetMetaEvalDefinition(ctx, organizationID, id)
}

func (s *Store) Evaluate(ctx context.Context, input EvaluateInput) (MetaEvaluation, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.DefinitionID = strings.TrimSpace(input.DefinitionID)
	if input.OrganizationID == "" || input.ActorID == "" || input.DefinitionID == "" {
		return MetaEvaluation{}, fmt.Errorf("organization, actor, and meta-eval definition are required")
	}
	if len(input.Samples) < 2 || len(input.Samples) > MaxSamples {
		return MetaEvaluation{}, fmt.Errorf("meta-evaluation requires between 2 and %d samples", MaxSamples)
	}
	definition, err := s.GetMetaEvalDefinition(ctx, input.OrganizationID, input.DefinitionID)
	if err != nil {
		return MetaEvaluation{}, err
	}
	samples, err := normalizeSamples(input.Samples)
	if err != nil {
		return MetaEvaluation{}, err
	}
	builders := map[string]*summaryBuilder{}
	for _, sample := range samples {
		if err := s.accumulateSample(ctx, input.OrganizationID, definition, sample, builders); err != nil {
			return MetaEvaluation{}, fmt.Errorf("sample %s: %w", sample.ExperimentID, err)
		}
	}
	strategies := make([]StrategySummary, 0, len(builders))
	for _, builder := range builders {
		strategies = append(strategies, builder.finish(definition))
	}
	sort.Slice(strategies, func(i, j int) bool { return strategySortKey(strategies[i].Strategy) < strategySortKey(strategies[j].Strategy) })
	pairwise := pairwiseViews(strategies)
	identity := struct {
		DefinitionRevision string                  `json:"definition_revision"`
		Samples            []EvaluationSampleInput `json:"samples"`
	}{definition.DefinitionRevision, samples}
	identityJSON, _ := json.Marshal(identity)
	id := contentID("impeval_", input.OrganizationID, identityJSON)
	if existing, err := s.GetMetaEvaluation(ctx, input.OrganizationID, id); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return MetaEvaluation{}, err
	}
	samplesJSON, _ := json.Marshal(samples)
	strategiesJSON, _ := json.Marshal(strategies)
	pairwiseJSON, _ := json.Marshal(pairwise)
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO improver_meta_evaluations(
		id, organization_id, definition_id, definition_revision, samples_json, strategies_json, pairwise_json, actor_id, correlation_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, input.OrganizationID, definition.ID, definition.DefinitionRevision,
		string(samplesJSON), string(strategiesJSON), string(pairwiseJSON), input.ActorID, input.CorrelationID)
	if err != nil {
		return MetaEvaluation{}, err
	}
	return s.GetMetaEvaluation(ctx, input.OrganizationID, id)
}

func (s *Store) GetMetaEvaluation(ctx context.Context, organizationID, id string) (MetaEvaluation, error) {
	organizationID, id = strings.TrimSpace(organizationID), strings.TrimSpace(id)
	if organizationID == "" || id == "" {
		return MetaEvaluation{}, fmt.Errorf("organization and meta-evaluation id are required")
	}
	var item MetaEvaluation
	var samplesJSON, strategiesJSON, pairwiseJSON string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id, organization_id, definition_id, definition_revision,
		samples_json, strategies_json, pairwise_json, actor_id, correlation_id, created_at
		FROM improver_meta_evaluations WHERE organization_id=? AND id=?`, organizationID, id).Scan(
		&item.ID, &item.OrganizationID, &item.DefinitionID, &item.DefinitionRevision,
		&samplesJSON, &strategiesJSON, &pairwiseJSON, &item.ActorID, &item.CorrelationID, &item.CreatedAt)
	if err != nil {
		return MetaEvaluation{}, err
	}
	if err := json.Unmarshal([]byte(samplesJSON), &item.Samples); err != nil {
		return MetaEvaluation{}, fmt.Errorf("decode meta-evaluation samples: %w", err)
	}
	if err := json.Unmarshal([]byte(strategiesJSON), &item.Strategies); err != nil {
		return MetaEvaluation{}, fmt.Errorf("decode meta-evaluation strategies: %w", err)
	}
	if err := json.Unmarshal([]byte(pairwiseJSON), &item.Pairwise); err != nil {
		return MetaEvaluation{}, fmt.Errorf("decode meta-evaluation pairwise view: %w", err)
	}
	return item, nil
}

type summaryBuilder struct {
	strategy             StrategyRef
	samples              int
	completed            int
	failed               int
	accepted             int
	rejected             int
	undecided            int
	development          DecisionCounts
	heldOut              DecisionCounts
	devGains             map[string]*metricAccumulator
	heldGains            map[string]*metricAccumulator
	costs                map[string]*metricAccumulator
	runtimes             map[string]*metricAccumulator
	usefulWithinBudget   []float64
	usefulPerCost        []float64
	usefulUnit           string
	budgetCompliant      int
	budgetExceeded       int
	budgetUnknown        int
	overfitCount         int
}

type metricAccumulator struct {
	kind, name, unit string
	values           []float64
}

func (s *Store) accumulateSample(ctx context.Context, organizationID string, definition MetaEvalDefinition, sample EvaluationSampleInput, builders map[string]*summaryBuilder) error {
	exp, err := s.Experiments.Get(ctx, organizationID, sample.ExperimentID)
	if err != nil {
		return err
	}
	if exp.Status != experiment.StatusCompleted && exp.Status != experiment.StatusFailed {
		return fmt.Errorf("experiment must be terminal completed or failed, got %s", exp.Status)
	}
	provenance, err := s.ProvenanceOrMissing(ctx, organizationID, exp.ID)
	if err != nil {
		return err
	}
	strategy := StrategyRef{ID: provenance.Strategy.ID, ConfigSHA256: provenance.Strategy.ConfigSHA256, ParentStrategyID: provenance.Strategy.ParentStrategyID}
	strategy.Comparable = strategy.ID.State == EvidenceKnown && strategy.ConfigSHA256.State == EvidenceKnown
	key := strategySortKey(strategy)
	builder := builders[key]
	if builder == nil {
		builder = &summaryBuilder{strategy: strategy, devGains: map[string]*metricAccumulator{}, heldGains: map[string]*metricAccumulator{}, costs: map[string]*metricAccumulator{}, runtimes: map[string]*metricAccumulator{}}
		builders[key] = builder
	}
	builder.samples++
	if exp.Status == experiment.StatusFailed {
		builder.failed++
		builder.undecided++
		return nil
	}
	builder.completed++
	if sample.DevelopmentComparisonID == "" || sample.HeldOutComparisonID == "" {
		return fmt.Errorf("completed experiment requires development and held-out comparisons")
	}
	dev, devChampion, devChallenger, err := s.loadComparison(ctx, organizationID, exp, sample.DevelopmentComparisonID, definition.DevelopmentScopes)
	if err != nil {
		return fmt.Errorf("development comparison: %w", err)
	}
	held, heldChampion, heldChallenger, err := s.loadComparison(ctx, organizationID, exp, sample.HeldOutComparisonID, definition.HeldOutScopes)
	if err != nil {
		return fmt.Errorf("held-out comparison: %w", err)
	}
	accumulateDecision(&builder.development, dev.Result.Decision)
	accumulateDecision(&builder.heldOut, held.Result.Decision)
	accumulateProtectedGains(builder.devGains, dev, devChampion, devChallenger)
	accumulateProtectedGains(builder.heldGains, held, heldChampion, heldChallenger)
	accumulateMetricKind(builder.costs, heldChallenger.Metrics, fitness.MetricCost)
	accumulateMetricKind(builder.runtimes, heldChallenger.Metrics, fitness.MetricRuntime)
	if dev.Result.Decision == fitness.DecisionPassesGate && held.Result.Decision != fitness.DecisionPassesGate {
		builder.overfitCount++
	}
	accepted, decided, err := s.acceptanceForSample(ctx, organizationID, exp, sample.LineageID)
	if err != nil {
		return err
	}
	if !decided {
		builder.undecided++
	} else if accepted {
		builder.accepted++
	} else {
		builder.rejected++
	}
	useful, usefulUnit, ok := usefulGain(definition.UsefulMetric, heldChampion, heldChallenger)
	within, known := withinBudget(definition.Budget, heldChallenger)
	if !known {
		builder.budgetUnknown++
	} else if !within {
		builder.budgetExceeded++
	} else {
		builder.budgetCompliant++
		if ok {
			builder.usefulWithinBudget = append(builder.usefulWithinBudget, useful)
			builder.usefulUnit = usefulUnit
			if definition.Budget.CostMetricName != "" {
				if cost, _, found := findMetric(heldChallenger.Metrics, fitness.MetricCost, definition.Budget.CostMetricName); found && cost.Value > 0 {
					builder.usefulPerCost = append(builder.usefulPerCost, useful/cost.Value)
				}
			}
		}
	}
	return nil
}

func (s *Store) loadComparison(ctx context.Context, organizationID string, exp experiment.Experiment, comparisonID string, allowed []fitness.EvalScope) (fitness.Comparison, fitness.Evidence, fitness.Evidence, error) {
	comparison, err := s.Fitness.GetComparison(ctx, organizationID, strings.TrimSpace(comparisonID))
	if err != nil {
		return fitness.Comparison{}, fitness.Evidence{}, fitness.Evidence{}, err
	}
	if comparison.CapabilityID != exp.CapabilityID {
		return fitness.Comparison{}, fitness.Evidence{}, fitness.Evidence{}, fmt.Errorf("comparison belongs to a different capability")
	}
	champion, err := s.Fitness.GetEvidence(ctx, organizationID, comparison.ChampionEvidenceID)
	if err != nil {
		return fitness.Comparison{}, fitness.Evidence{}, fitness.Evidence{}, err
	}
	challenger, err := s.Fitness.GetEvidence(ctx, organizationID, comparison.ChallengerEvidenceID)
	if err != nil {
		return fitness.Comparison{}, fitness.Evidence{}, fitness.Evidence{}, err
	}
	if !scopeEqual(champion.Scope, challenger.Scope) || !scopeAllowed(challenger.Scope, allowed) {
		return fitness.Comparison{}, fitness.Evidence{}, fitness.Evidence{}, fmt.Errorf("comparison scope is outside the protected meta-eval distribution")
	}
	if !contains(challenger.ExperimentIDs, exp.ID) {
		return fitness.Comparison{}, fitness.Evidence{}, fitness.Evidence{}, fmt.Errorf("challenger evidence is not bound to the experiment")
	}
	return comparison, champion, challenger, nil
}

func (s *Store) acceptanceForSample(ctx context.Context, organizationID string, exp experiment.Experiment, lineageID string) (bool, bool, error) {
	lineageID = strings.TrimSpace(lineageID)
	if lineageID == "" {
		return false, false, nil
	}
	entry, err := s.Lineage.Get(ctx, organizationID, lineageID)
	if err != nil {
		return false, false, err
	}
	if !contains(entry.Record.ExperimentIDs, exp.ID) {
		return false, false, fmt.Errorf("lineage record is not bound to the experiment")
	}
	if entry.Record.CapabilityID != exp.CapabilityID {
		return false, false, fmt.Errorf("lineage record belongs to a different capability")
	}
	if entry.Record.DescendantKind == lineage.DescendantCandidate && entry.Record.DescendantID != exp.Origin.CandidateID {
		return false, false, fmt.Errorf("candidate lineage does not match the experiment candidate")
	}
	if entry.Decision == nil {
		return false, false, nil
	}
	return entry.Decision.State == lineage.DecisionPromoted, true, nil
}

func (b *summaryBuilder) finish(definition MetaEvalDefinition) StrategySummary {
	var acceptance *float64
	if decided := b.accepted + b.rejected; decided > 0 {
		value := float64(b.accepted) / float64(decided)
		acceptance = &value
	}
	return StrategySummary{
		Strategy: b.strategy, SampleCount: b.samples, CompletedExperiments: b.completed, FailedExperiments: b.failed,
		AcceptedCandidates: b.accepted, RejectedCandidates: b.rejected, UndecidedCandidates: b.undecided, AcceptanceRate: acceptance,
		DevelopmentDecisions: b.development, HeldOutDecisions: b.heldOut,
		ProtectedDevelopmentGain: finishMetricMap(b.devGains), ProtectedHeldOutGain: finishMetricMap(b.heldGains),
		CostPerCompletedExperiment: finishMetricMap(b.costs), RuntimePerCompletedExperiment: finishMetricMap(b.runtimes),
		UsefulGainWithinBudget: MetricDistribution{Kind: definition.UsefulMetric.Kind, Name: definition.UsefulMetric.Name, Unit: b.usefulUnit, Distribution: finishDistribution(b.usefulWithinBudget)},
		UsefulGainPerCostWithinBudget: MetricDistribution{Kind: "derived", Name: "useful_gain_per_cost", Unit: b.usefulUnit + "/" + definition.Budget.CostUnit, Distribution: finishDistribution(b.usefulPerCost)},
		BudgetCompliantCompleted: b.budgetCompliant, BudgetExceededCompleted: b.budgetExceeded, BudgetUnknownCompleted: b.budgetUnknown,
		DevelopmentPassHeldOutFailCount: b.overfitCount, OverfitWarning: b.overfitCount > 0,
	}
}

func pairwiseViews(strategies []StrategySummary) []PairwiseView {
	var out []PairwiseView
	for i := 0; i < len(strategies); i++ {
		for j := i + 1; j < len(strategies); j++ {
			a, b := strategies[i], strategies[j]
			view := PairwiseView{StrategyA: a.Strategy, StrategyB: b.Strategy, Conclusion: "scoped_evidence_only", Reason: "No universal improver ranking is produced; inspect acceptance, failures, protected gains, budget evidence, and held-out results within this exact meta-eval definition."}
			if !a.Strategy.Comparable || !b.Strategy.Comparable {
				view.Conclusion = "incomparable_provenance"
				view.Reason = "At least one strategy is missing or has redacted identity/config provenance, so superiority is not inferred."
			} else if a.OverfitWarning || b.OverfitWarning {
				view.Conclusion = "held_out_regression_prevents_superiority_claim"
				view.Reason = "Development success did not hold on protected held-out evidence for at least one strategy; no superiority claim is emitted."
			}
			out = append(out, view)
		}
	}
	return out
}

func normalizeProvenance(item *Provenance, configState, configJSON string) error {
	values := []*EvidenceValue{
		&item.Agent.Identity, &item.Agent.Version, &item.Harness.Identity, &item.Harness.Version,
		&item.Model.Provider, &item.Model.Model, &item.Model.Revision, &item.SystemRevision, &item.PromptRevision,
		&item.SkillBundleRevision, &item.Strategy.ID, &item.Strategy.ParentStrategyID,
		&item.ContextPolicyRevision, &item.CurriculumPolicyRevision,
	}
	for _, value := range values {
		if err := normalizeEvidenceValue(value); err != nil {
			return err
		}
	}
	if err := normalizeComponents(&item.ToolAdapters); err != nil {
		return err
	}
	state := strings.TrimSpace(configState)
	if state == "" {
		state = EvidenceMissing
	}
	item.Strategy.ConfigSHA256 = EvidenceValue{State: state}
	switch state {
	case EvidenceMissing, EvidenceRedacted:
		item.Strategy.ConfigSHA256.Value = ""
	case EvidenceKnown:
		canonical, err := canonicalJSON(configJSON)
		if err != nil {
			return fmt.Errorf("strategy config: %w", err)
		}
		sum := sha256.Sum256(canonical)
		item.Strategy.ConfigSHA256.Value = hex.EncodeToString(sum[:])
	default:
		return fmt.Errorf("strategy config state must be known, missing, or redacted")
	}
	return nil
}

func normalizeEvidenceValue(value *EvidenceValue) error {
	value.State = strings.TrimSpace(value.State)
	if value.State == "" {
		value.State = EvidenceMissing
	}
	value.Value = strings.TrimSpace(value.Value)
	switch value.State {
	case EvidenceKnown:
		if err := validateText("provenance value", value.Value, MaxIdentityBytes, true); err != nil {
			return err
		}
	case EvidenceMissing, EvidenceRedacted:
		value.Value = ""
	default:
		return fmt.Errorf("provenance state must be known, missing, or redacted")
	}
	return nil
}

func normalizeComponents(set *ComponentSet) error {
	set.State = strings.TrimSpace(set.State)
	if set.State == "" {
		set.State = EvidenceMissing
	}
	switch set.State {
	case EvidenceMissing, EvidenceRedacted:
		set.Components = nil
		return nil
	case EvidenceKnown:
		if len(set.Components) == 0 || len(set.Components) > MaxComponents {
			return fmt.Errorf("known tool/adaptor provenance must contain between 1 and %d components", MaxComponents)
		}
	default:
		return fmt.Errorf("tool/adaptor state must be known, missing, or redacted")
	}
	seen := map[string]struct{}{}
	for i := range set.Components {
		set.Components[i].Name = strings.TrimSpace(set.Components[i].Name)
		if err := validateText("component name", set.Components[i].Name, MaxIdentityBytes, true); err != nil {
			return err
		}
		if _, ok := seen[set.Components[i].Name]; ok {
			return fmt.Errorf("duplicate tool/adaptor component %q", set.Components[i].Name)
		}
		seen[set.Components[i].Name] = struct{}{}
		if err := normalizeEvidenceValue(&set.Components[i].Version); err != nil {
			return err
		}
	}
	sort.Slice(set.Components, func(i, j int) bool { return set.Components[i].Name < set.Components[j].Name })
	return nil
}

func canonicalJSON(raw string) ([]byte, error) {
	if len(raw) == 0 || len(raw) > MaxStrategyConfig {
		return nil, fmt.Errorf("known strategy config must contain between 1 and %d bytes", MaxStrategyConfig)
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("strategy config contains trailing JSON values")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != nil && !errors.Is(err, bytes.ErrTooLarge) {
		// A second successful decode would be a trailing value. io.EOF is expected,
		// but importing io only for that sentinel is unnecessary because any nil
		// error below is the unsafe case.
		if !strings.Contains(err.Error(), "EOF") {
			return nil, fmt.Errorf("decode strategy config trailer: %w", err)
		}
	} else if err == nil {
		return nil, fmt.Errorf("strategy config contains trailing JSON values")
	}
	return json.Marshal(value)
}

func normalizeScopes(scopes []fitness.EvalScope) ([]fitness.EvalScope, error) {
	if len(scopes) == 0 || len(scopes) > MaxScopes {
		return nil, fmt.Errorf("scope count must be between 1 and %d", MaxScopes)
	}
	out := append([]fitness.EvalScope(nil), scopes...)
	for i := range out {
		fields := []*string{&out[i].EvalSuiteID, &out[i].EvalSuiteVersion, &out[i].TaskDistributionID, &out[i].TaskDistributionVersion}
		for _, field := range fields {
			*field = strings.TrimSpace(*field)
			if err := validateText("scope identity", *field, MaxIdentityBytes, true); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return scopeKey(out[i]) < scopeKey(out[j]) })
	for i := 1; i < len(out); i++ {
		if scopeEqual(out[i-1], out[i]) {
			return nil, fmt.Errorf("duplicate meta-eval scope")
		}
	}
	return out, nil
}

func normalizeMetricSelector(selector MetricSelector) (MetricSelector, error) {
	selector.Kind = strings.TrimSpace(selector.Kind)
	selector.Name = strings.TrimSpace(selector.Name)
	if selector.Kind == "" || selector.Name == "" {
		return MetricSelector{}, fmt.Errorf("useful metric kind and name are required")
	}
	switch selector.Kind {
	case fitness.MetricQuality, fitness.MetricRegression, fitness.MetricCost, fitness.MetricRuntime, fitness.MetricResource:
	default:
		return MetricSelector{}, fmt.Errorf("unsupported useful metric kind %q", selector.Kind)
	}
	if err := validateText("useful metric name", selector.Name, MaxIdentityBytes, true); err != nil {
		return MetricSelector{}, err
	}
	return selector, nil
}

func normalizeBudget(budget BudgetGuard) (BudgetGuard, error) {
	budget.CostMetricName, budget.CostUnit = strings.TrimSpace(budget.CostMetricName), strings.TrimSpace(budget.CostUnit)
	budget.RuntimeMetricName, budget.RuntimeUnit = strings.TrimSpace(budget.RuntimeMetricName), strings.TrimSpace(budget.RuntimeUnit)
	if math.IsNaN(budget.MaxCost) || math.IsInf(budget.MaxCost, 0) || budget.MaxCost < 0 || math.IsNaN(budget.MaxRuntime) || math.IsInf(budget.MaxRuntime, 0) || budget.MaxRuntime < 0 {
		return BudgetGuard{}, fmt.Errorf("meta-eval budget limits must be finite and non-negative")
	}
	if budget.CostMetricName != "" {
		if budget.CostUnit == "" || budget.MaxCost <= 0 {
			return BudgetGuard{}, fmt.Errorf("cost budget requires metric name, unit, and positive maximum")
		}
	}
	if budget.RuntimeMetricName != "" {
		if budget.RuntimeUnit == "" || budget.MaxRuntime <= 0 {
			return BudgetGuard{}, fmt.Errorf("runtime budget requires metric name, unit, and positive maximum")
		}
	}
	return budget, nil
}

func normalizeSamples(samples []EvaluationSampleInput) ([]EvaluationSampleInput, error) {
	out := append([]EvaluationSampleInput(nil), samples...)
	seen := map[string]struct{}{}
	for i := range out {
		out[i].ExperimentID = strings.TrimSpace(out[i].ExperimentID)
		out[i].LineageID = strings.TrimSpace(out[i].LineageID)
		out[i].DevelopmentComparisonID = strings.TrimSpace(out[i].DevelopmentComparisonID)
		out[i].HeldOutComparisonID = strings.TrimSpace(out[i].HeldOutComparisonID)
		if out[i].ExperimentID == "" {
			return nil, fmt.Errorf("sample experiment id is required")
		}
		if _, ok := seen[out[i].ExperimentID]; ok {
			return nil, fmt.Errorf("duplicate experiment sample %q", out[i].ExperimentID)
		}
		seen[out[i].ExperimentID] = struct{}{}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExperimentID < out[j].ExperimentID })
	return out, nil
}

func accumulateDecision(counts *DecisionCounts, decision string) {
	switch decision {
	case fitness.DecisionPassesGate:
		counts.Passes++
	case fitness.DecisionFailsGate:
		counts.Fails++
	default:
		counts.Inconclusive++
	}
}

func accumulateProtectedGains(target map[string]*metricAccumulator, comparison fitness.Comparison, champion, challenger fitness.Evidence) {
	for _, criterion := range comparison.Result.Criteria {
		if !criterion.Protected {
			continue
		}
		champ, champUnit, champOK := findMetric(champion.Metrics, criterion.MetricKind, criterion.MetricName)
		challenge, challengeUnit, challengeOK := findMetric(challenger.Metrics, criterion.MetricKind, criterion.MetricName)
		if !champOK || !challengeOK || champUnit != challengeUnit {
			continue
		}
		key := criterion.MetricKind + "\x00" + criterion.MetricName + "\x00" + champUnit
		acc := target[key]
		if acc == nil {
			acc = &metricAccumulator{kind: criterion.MetricKind, name: criterion.MetricName, unit: champUnit}
			target[key] = acc
		}
		acc.values = append(acc.values, challenge.Value-champ.Value)
	}
}

func accumulateMetricKind(target map[string]*metricAccumulator, metrics []fitness.Metric, kind string) {
	for _, metric := range metrics {
		if metric.Kind != kind {
			continue
		}
		key := metric.Kind + "\x00" + metric.Name + "\x00" + metric.Unit
		acc := target[key]
		if acc == nil {
			acc = &metricAccumulator{kind: metric.Kind, name: metric.Name, unit: metric.Unit}
			target[key] = acc
		}
		acc.values = append(acc.values, metric.Value)
	}
}

func usefulGain(selector MetricSelector, champion, challenger fitness.Evidence) (float64, string, bool) {
	champ, champUnit, champOK := findMetric(champion.Metrics, selector.Kind, selector.Name)
	challenge, challengeUnit, challengeOK := findMetric(challenger.Metrics, selector.Kind, selector.Name)
	if !champOK || !challengeOK || champUnit != challengeUnit {
		return 0, "", false
	}
	if selector.HigherIsBetter {
		return challenge.Value - champ.Value, champUnit, true
	}
	return champ.Value - challenge.Value, champUnit, true
}

func withinBudget(budget BudgetGuard, evidence fitness.Evidence) (bool, bool) {
	known := true
	within := true
	if budget.CostMetricName != "" {
		metric, unit, ok := findMetric(evidence.Metrics, fitness.MetricCost, budget.CostMetricName)
		if !ok || unit != budget.CostUnit {
			known = false
		} else if metric.Value > budget.MaxCost {
			within = false
		}
	}
	if budget.RuntimeMetricName != "" {
		metric, unit, ok := findMetric(evidence.Metrics, fitness.MetricRuntime, budget.RuntimeMetricName)
		if !ok || unit != budget.RuntimeUnit {
			known = false
		} else if metric.Value > budget.MaxRuntime {
			within = false
		}
	}
	if budget.CostMetricName == "" && budget.RuntimeMetricName == "" {
		return true, true
	}
	return within, known
}

func findMetric(metrics []fitness.Metric, kind, name string) (fitness.Metric, string, bool) {
	for _, metric := range metrics {
		if metric.Kind == kind && metric.Name == name {
			return metric, metric.Unit, true
		}
	}
	return fitness.Metric{}, "", false
}

func finishMetricMap(input map[string]*metricAccumulator) []MetricDistribution {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]MetricDistribution, 0, len(keys))
	for _, key := range keys {
		item := input[key]
		out = append(out, MetricDistribution{Kind: item.kind, Name: item.name, Unit: item.unit, Distribution: finishDistribution(item.values)})
	}
	return out
}

func finishDistribution(values []float64) Distribution {
	if len(values) == 0 {
		return Distribution{}
	}
	values = append([]float64(nil), values...)
	sort.Float64s(values)
	median := values[len(values)/2]
	if len(values)%2 == 0 {
		median = (values[len(values)/2-1] + values[len(values)/2]) / 2
	}
	return Distribution{Count: len(values), Min: values[0], Median: median, Max: values[len(values)-1], Values: values}
}

func scopeAllowed(scope fitness.EvalScope, allowed []fitness.EvalScope) bool {
	for _, candidate := range allowed {
		if scopeEqual(scope, candidate) {
			return true
		}
	}
	return false
}

func scopeEqual(a, b fitness.EvalScope) bool {
	return a.EvalSuiteID == b.EvalSuiteID && a.EvalSuiteVersion == b.EvalSuiteVersion && a.TaskDistributionID == b.TaskDistributionID && a.TaskDistributionVersion == b.TaskDistributionVersion
}

func scopeKey(scope fitness.EvalScope) string {
	return scope.EvalSuiteID + "\x00" + scope.EvalSuiteVersion + "\x00" + scope.TaskDistributionID + "\x00" + scope.TaskDistributionVersion
}

func strategySortKey(strategy StrategyRef) string {
	if strategy.Comparable {
		return "known\x00" + strategy.ID.Value + "\x00" + strategy.ConfigSHA256.Value
	}
	return "unattributed\x00" + strategy.ID.State + "\x00" + strategy.ConfigSHA256.State
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func contentID(prefix, organizationID string, payload []byte) string {
	sum := sha256.Sum256(append(append([]byte(organizationID), 0), payload...))
	return prefix + hex.EncodeToString(sum[:16])
}

func validateText(name, value string, max int, required bool) error {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !utf8.ValidString(value) || len(value) > max {
		return fmt.Errorf("%s must be valid UTF-8 and at most %d bytes", name, max)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}
