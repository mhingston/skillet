// Package fitness owns opt-in, append-only scoped fitness evidence and
// deterministic champion/challenger gate results. Fitness evidence never
// participates in normal retrieval ranking, activation, governance, or source
// mutation.
package fitness

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/experiment"
)

const (
	MetricQuality    = "quality"
	MetricRegression = "regression"
	MetricCost       = "cost"
	MetricRuntime    = "runtime"
	MetricResource   = "resource"

	ComparatorDeltaGTE      = "delta_gte"
	ComparatorDeltaLTE      = "delta_lte"
	ComparatorChallengerGTE = "challenger_gte"
	ComparatorChallengerLTE = "challenger_lte"

	DecisionPassesGate  = "passes_gate"
	DecisionFailsGate   = "fails_gate"
	DecisionInconclusive = "inconclusive"

	CriterionPass         = "pass"
	CriterionFail         = "fail"
	CriterionInconclusive = "inconclusive"

	MaxMetrics       = 128
	MaxCriteria      = 64
	MaxExperiments   = 64
	MaxProvenance    = 64
	MaxIdentityBytes = 256
	MaxReferenceBytes = 2 * 1024
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type RevisionRef struct {
	CapabilityID string `json:"capability_id"`
	RevisionID   string `json:"revision_id"`
	Commit       string `json:"commit"`
	Tree         string `json:"tree"`
}

type EvalScope struct {
	EvalSuiteID             string `json:"eval_suite_id"`
	EvalSuiteVersion        string `json:"eval_suite_version"`
	TaskDistributionID      string `json:"task_distribution_id"`
	TaskDistributionVersion string `json:"task_distribution_version"`
}

type ExecutionIdentity struct {
	Agent     string `json:"agent,omitempty"`
	Model     string `json:"model"`
	Harness   string `json:"harness"`
	Toolchain string `json:"toolchain"`
}

type RunMetadata struct {
	RunID           string `json:"run_id"`
	Seed            string `json:"seed"`
	SampleCount     int64  `json:"sample_count"`
	SampleSetSHA256 string `json:"sample_set_sha256"`
}

type Uncertainty struct {
	Method     string  `json:"method"`
	Confidence float64 `json:"confidence"`
	Lower      float64 `json:"lower"`
	Upper      float64 `json:"upper"`
}

type Metric struct {
	Kind        string       `json:"kind"`
	Name        string       `json:"name"`
	Value       float64      `json:"value"`
	Unit        string       `json:"unit"`
	Uncertainty *Uncertainty `json:"uncertainty,omitempty"`
	EvidenceRef string       `json:"evidence_ref,omitempty"`
}

type ProvenanceReference struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	SHA256    string `json:"sha256,omitempty"`
}

type Evidence struct {
	ID              string                `json:"id"`
	OrganizationID  string                `json:"organization_id"`
	Revision        RevisionRef           `json:"revision"`
	Scope           EvalScope             `json:"scope"`
	Executor        ExecutionIdentity     `json:"executor"`
	Run             RunMetadata           `json:"run"`
	Metrics         []Metric              `json:"metrics"`
	ExperimentIDs   []string              `json:"experiment_ids,omitempty"`
	Provenance      []ProvenanceReference `json:"provenance,omitempty"`
	ActorID         string                `json:"actor_id"`
	CorrelationID   string                `json:"correlation_id,omitempty"`
	CreatedAt       string                `json:"created_at"`
}

type RecordEvidenceInput struct {
	OrganizationID string
	ActorID        string
	CorrelationID  string
	RevisionID     string
	Scope          EvalScope
	Executor       ExecutionIdentity
	Run            RunMetadata
	Metrics        []Metric
	ExperimentIDs  []string
	Provenance     []ProvenanceReference
}

type Criterion struct {
	MetricKind        string  `json:"metric_kind"`
	MetricName        string  `json:"metric_name"`
	Comparator        string  `json:"comparator"`
	Threshold         float64 `json:"threshold"`
	Protected         bool    `json:"protected,omitempty"`
	MinimumConfidence float64 `json:"minimum_confidence,omitempty"`
}

type PromotionPolicy struct {
	ID             string        `json:"id"`
	OrganizationID string        `json:"organization_id"`
	Champion       RevisionRef   `json:"champion"`
	Scope          EvalScope     `json:"scope"`
	Criteria       []Criterion   `json:"criteria"`
	PolicyRevision string        `json:"policy_revision"`
	ActorID        string        `json:"actor_id"`
	CorrelationID  string        `json:"correlation_id,omitempty"`
	CreatedAt      string        `json:"created_at"`
}

type CreatePolicyInput struct {
	OrganizationID     string
	ActorID            string
	CorrelationID      string
	ChampionRevisionID string
	Scope              EvalScope
	Criteria           []Criterion
}

type CriterionResult struct {
	MetricKind       string  `json:"metric_kind"`
	MetricName       string  `json:"metric_name"`
	Comparator       string  `json:"comparator"`
	Threshold        float64 `json:"threshold"`
	Protected        bool    `json:"protected,omitempty"`
	ChampionValue    *float64 `json:"champion_value,omitempty"`
	ChallengerValue  *float64 `json:"challenger_value,omitempty"`
	ObservedDelta    *float64 `json:"observed_delta,omitempty"`
	EvaluationLower  *float64 `json:"evaluation_lower,omitempty"`
	EvaluationUpper  *float64 `json:"evaluation_upper,omitempty"`
	Status           string  `json:"status"`
	Reason           string  `json:"reason,omitempty"`
}

type ComparisonResult struct {
	Decision       string            `json:"decision"`
	PolicyID       string            `json:"policy_id"`
	PolicyRevision string            `json:"policy_revision"`
	Criteria       []CriterionResult `json:"criteria"`
}

type Comparison struct {
	ID                   string           `json:"id"`
	OrganizationID       string           `json:"organization_id"`
	CapabilityID         string           `json:"capability_id"`
	ChampionRevisionID   string           `json:"champion_revision_id"`
	ChallengerRevisionID string           `json:"challenger_revision_id"`
	ChampionEvidenceID   string           `json:"champion_evidence_id"`
	ChallengerEvidenceID string           `json:"challenger_evidence_id"`
	Result               ComparisonResult `json:"result"`
	ActorID              string           `json:"actor_id"`
	CorrelationID        string           `json:"correlation_id,omitempty"`
	CreatedAt            string           `json:"created_at"`
}

type CompareInput struct {
	OrganizationID       string
	ActorID              string
	CorrelationID        string
	PolicyID             string
	ChampionEvidenceID   string
	ChallengerEvidenceID string
}

type Store struct {
	Catalogue *catalogue.Store
}

func New(ctx context.Context, catalog *catalogue.Store) (*Store, error) {
	if catalog == nil || catalog.DB == nil {
		return nil, fmt.Errorf("fitness catalogue is required")
	}
	// M4.3 may reference M4.1 experiments even when the experiment MCP surface is
	// disabled in this process. Initialising its local schema does not execute or
	// expose anything.
	if _, err := experiment.New(ctx, catalog); err != nil {
		return nil, err
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS fitness_evidence (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			capability_id TEXT NOT NULL,
			revision_id TEXT NOT NULL,
			commit_sha TEXT NOT NULL,
			tree_sha TEXT NOT NULL,
			scope_json TEXT NOT NULL,
			executor_json TEXT NOT NULL,
			run_json TEXT NOT NULL,
			metrics_json TEXT NOT NULL,
			experiment_ids_json TEXT NOT NULL,
			provenance_json TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_fitness_evidence_revision ON fitness_evidence(organization_id, revision_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS fitness_promotion_policies (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			capability_id TEXT NOT NULL,
			champion_revision_id TEXT NOT NULL,
			champion_commit_sha TEXT NOT NULL,
			champion_tree_sha TEXT NOT NULL,
			scope_json TEXT NOT NULL,
			criteria_json TEXT NOT NULL,
			policy_revision TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(organization_id, policy_revision)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_fitness_policy_champion ON fitness_promotion_policies(organization_id, champion_revision_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS fitness_comparisons (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			capability_id TEXT NOT NULL,
			champion_revision_id TEXT NOT NULL,
			challenger_revision_id TEXT NOT NULL,
			champion_evidence_id TEXT NOT NULL REFERENCES fitness_evidence(id),
			challenger_evidence_id TEXT NOT NULL REFERENCES fitness_evidence(id),
			policy_id TEXT NOT NULL REFERENCES fitness_promotion_policies(id),
			result_json TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_fitness_comparison_revision ON fitness_comparisons(organization_id, champion_revision_id, challenger_revision_id, created_at, id)`,
	}
	for _, statement := range statements {
		if _, err := catalog.DB.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize fitness schema: %w", err)
		}
	}
	return &Store{Catalogue: catalog}, nil
}

func (s *Store) RecordEvidence(ctx context.Context, input RecordEvidenceInput) (Evidence, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.RevisionID = strings.TrimSpace(input.RevisionID)
	if input.OrganizationID == "" || input.ActorID == "" || input.RevisionID == "" {
		return Evidence{}, fmt.Errorf("organization, actor, and revision are required")
	}
	if err := validateIdentity("actor", input.ActorID, true); err != nil {
		return Evidence{}, err
	}
	if input.CorrelationID != "" {
		if err := validateIdentity("correlation id", input.CorrelationID, true); err != nil {
			return Evidence{}, err
		}
	}
	info, err := s.Catalogue.Revision(ctx, input.OrganizationID, input.RevisionID)
	if err != nil {
		return Evidence{}, err
	}
	revision := RevisionRef{CapabilityID: info.SkillID, RevisionID: info.RevisionID, Commit: info.Commit, Tree: info.Tree}
	if err := validateScope(&input.Scope); err != nil {
		return Evidence{}, err
	}
	if err := validateExecutor(&input.Executor); err != nil {
		return Evidence{}, err
	}
	if err := validateRun(&input.Run); err != nil {
		return Evidence{}, err
	}
	metrics, err := normalizeMetrics(input.Metrics)
	if err != nil {
		return Evidence{}, err
	}
	experimentIDs, err := normalizeExperimentIDs(input.ExperimentIDs)
	if err != nil {
		return Evidence{}, err
	}
	if err := s.validateExperiments(ctx, input.OrganizationID, revision.CapabilityID, input.Scope, experimentIDs); err != nil {
		return Evidence{}, err
	}
	provenance, err := normalizeProvenance(input.Provenance)
	if err != nil {
		return Evidence{}, err
	}

	payload := struct {
		Revision      RevisionRef           `json:"revision"`
		Scope         EvalScope             `json:"scope"`
		Executor      ExecutionIdentity     `json:"executor"`
		Run           RunMetadata           `json:"run"`
		Metrics       []Metric              `json:"metrics"`
		ExperimentIDs []string              `json:"experiment_ids,omitempty"`
		Provenance    []ProvenanceReference `json:"provenance,omitempty"`
	}{revision, input.Scope, input.Executor, input.Run, metrics, experimentIDs, provenance}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Evidence{}, err
	}
	id := contentID("fit_", input.OrganizationID, payloadJSON)
	if existing, err := s.GetEvidence(ctx, input.OrganizationID, id); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Evidence{}, err
	}

	scopeJSON, _ := json.Marshal(input.Scope)
	executorJSON, _ := json.Marshal(input.Executor)
	runJSON, _ := json.Marshal(input.Run)
	metricsJSON, _ := json.Marshal(metrics)
	experimentsJSON, _ := json.Marshal(experimentIDs)
	provenanceJSON, _ := json.Marshal(provenance)
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO fitness_evidence(
		id, organization_id, capability_id, revision_id, commit_sha, tree_sha,
		scope_json, executor_json, run_json, metrics_json, experiment_ids_json,
		provenance_json, actor_id, correlation_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.OrganizationID, revision.CapabilityID, revision.RevisionID, revision.Commit, revision.Tree,
		string(scopeJSON), string(executorJSON), string(runJSON), string(metricsJSON), string(experimentsJSON),
		string(provenanceJSON), input.ActorID, input.CorrelationID)
	if err != nil {
		return Evidence{}, err
	}
	return s.GetEvidence(ctx, input.OrganizationID, id)
}

func (s *Store) GetEvidence(ctx context.Context, organizationID, id string) (Evidence, error) {
	organizationID, id = strings.TrimSpace(organizationID), strings.TrimSpace(id)
	if organizationID == "" || id == "" {
		return Evidence{}, fmt.Errorf("organization and fitness evidence id are required")
	}
	var item Evidence
	var scopeJSON, executorJSON, runJSON, metricsJSON, experimentsJSON, provenanceJSON string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id, organization_id, capability_id, revision_id, commit_sha, tree_sha,
		scope_json, executor_json, run_json, metrics_json, experiment_ids_json, provenance_json,
		actor_id, correlation_id, created_at FROM fitness_evidence WHERE organization_id=? AND id=?`, organizationID, id).Scan(
		&item.ID, &item.OrganizationID, &item.Revision.CapabilityID, &item.Revision.RevisionID, &item.Revision.Commit, &item.Revision.Tree,
		&scopeJSON, &executorJSON, &runJSON, &metricsJSON, &experimentsJSON, &provenanceJSON,
		&item.ActorID, &item.CorrelationID, &item.CreatedAt)
	if err != nil {
		return Evidence{}, err
	}
	for _, target := range []struct {
		name string
		raw  string
		out  any
	}{
		{"scope", scopeJSON, &item.Scope}, {"executor", executorJSON, &item.Executor}, {"run", runJSON, &item.Run},
		{"metrics", metricsJSON, &item.Metrics}, {"experiment ids", experimentsJSON, &item.ExperimentIDs}, {"provenance", provenanceJSON, &item.Provenance},
	} {
		if err := json.Unmarshal([]byte(target.raw), target.out); err != nil {
			return Evidence{}, fmt.Errorf("decode fitness %s: %w", target.name, err)
		}
	}
	return item, nil
}

func (s *Store) ListEvidence(ctx context.Context, organizationID, revisionID string, limit int) ([]Evidence, error) {
	organizationID, revisionID = strings.TrimSpace(organizationID), strings.TrimSpace(revisionID)
	if organizationID == "" || revisionID == "" {
		return nil, fmt.Errorf("organization and revision are required")
	}
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("fitness evidence limit must be between 1 and 100")
	}
	if _, err := s.Catalogue.Revision(ctx, organizationID, revisionID); err != nil {
		return nil, err
	}
	rows, err := s.Catalogue.DB.QueryContext(ctx, `SELECT id FROM fitness_evidence WHERE organization_id=? AND revision_id=? ORDER BY created_at DESC, id LIMIT ?`, organizationID, revisionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := make([]Evidence, 0, len(ids))
	for _, id := range ids {
		item, err := s.GetEvidence(ctx, organizationID, id)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) CreatePolicy(ctx context.Context, input CreatePolicyInput) (PromotionPolicy, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.ChampionRevisionID = strings.TrimSpace(input.ChampionRevisionID)
	if input.OrganizationID == "" || input.ActorID == "" || input.ChampionRevisionID == "" {
		return PromotionPolicy{}, fmt.Errorf("organization, actor, and champion revision are required")
	}
	if err := validateIdentity("actor", input.ActorID, true); err != nil {
		return PromotionPolicy{}, err
	}
	if err := validateScope(&input.Scope); err != nil {
		return PromotionPolicy{}, err
	}
	criteria, err := normalizeCriteria(input.Criteria)
	if err != nil {
		return PromotionPolicy{}, err
	}
	info, err := s.Catalogue.Revision(ctx, input.OrganizationID, input.ChampionRevisionID)
	if err != nil {
		return PromotionPolicy{}, err
	}
	champion := RevisionRef{CapabilityID: info.SkillID, RevisionID: info.RevisionID, Commit: info.Commit, Tree: info.Tree}
	payload := struct {
		Champion RevisionRef `json:"champion"`
		Scope    EvalScope   `json:"scope"`
		Criteria []Criterion `json:"criteria"`
	}{champion, input.Scope, criteria}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return PromotionPolicy{}, err
	}
	full := sha256.Sum256(payloadJSON)
	policyRevision := "sha256:" + hex.EncodeToString(full[:])
	id := contentID("fitpol_", input.OrganizationID, payloadJSON)
	if existing, err := s.GetPolicy(ctx, input.OrganizationID, id); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return PromotionPolicy{}, err
	}
	scopeJSON, _ := json.Marshal(input.Scope)
	criteriaJSON, _ := json.Marshal(criteria)
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO fitness_promotion_policies(
		id, organization_id, capability_id, champion_revision_id, champion_commit_sha, champion_tree_sha,
		scope_json, criteria_json, policy_revision, actor_id, correlation_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, input.OrganizationID, champion.CapabilityID,
		champion.RevisionID, champion.Commit, champion.Tree, string(scopeJSON), string(criteriaJSON), policyRevision,
		input.ActorID, input.CorrelationID)
	if err != nil {
		return PromotionPolicy{}, err
	}
	return s.GetPolicy(ctx, input.OrganizationID, id)
}

func (s *Store) GetPolicy(ctx context.Context, organizationID, id string) (PromotionPolicy, error) {
	organizationID, id = strings.TrimSpace(organizationID), strings.TrimSpace(id)
	if organizationID == "" || id == "" {
		return PromotionPolicy{}, fmt.Errorf("organization and fitness policy id are required")
	}
	var item PromotionPolicy
	var scopeJSON, criteriaJSON string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id, organization_id, capability_id, champion_revision_id,
		champion_commit_sha, champion_tree_sha, scope_json, criteria_json, policy_revision, actor_id, correlation_id, created_at
		FROM fitness_promotion_policies WHERE organization_id=? AND id=?`, organizationID, id).Scan(
		&item.ID, &item.OrganizationID, &item.Champion.CapabilityID, &item.Champion.RevisionID,
		&item.Champion.Commit, &item.Champion.Tree, &scopeJSON, &criteriaJSON, &item.PolicyRevision,
		&item.ActorID, &item.CorrelationID, &item.CreatedAt)
	if err != nil {
		return PromotionPolicy{}, err
	}
	if err := json.Unmarshal([]byte(scopeJSON), &item.Scope); err != nil {
		return PromotionPolicy{}, fmt.Errorf("decode fitness policy scope: %w", err)
	}
	if err := json.Unmarshal([]byte(criteriaJSON), &item.Criteria); err != nil {
		return PromotionPolicy{}, fmt.Errorf("decode fitness policy criteria: %w", err)
	}
	return item, nil
}

func (s *Store) Compare(ctx context.Context, input CompareInput) (Comparison, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.PolicyID = strings.TrimSpace(input.PolicyID)
	input.ChampionEvidenceID = strings.TrimSpace(input.ChampionEvidenceID)
	input.ChallengerEvidenceID = strings.TrimSpace(input.ChallengerEvidenceID)
	if input.OrganizationID == "" || input.ActorID == "" || input.PolicyID == "" || input.ChampionEvidenceID == "" || input.ChallengerEvidenceID == "" {
		return Comparison{}, fmt.Errorf("organization, actor, policy, champion evidence, and challenger evidence are required")
	}
	policy, err := s.GetPolicy(ctx, input.OrganizationID, input.PolicyID)
	if err != nil {
		return Comparison{}, err
	}
	champion, err := s.GetEvidence(ctx, input.OrganizationID, input.ChampionEvidenceID)
	if err != nil {
		return Comparison{}, err
	}
	challenger, err := s.GetEvidence(ctx, input.OrganizationID, input.ChallengerEvidenceID)
	if err != nil {
		return Comparison{}, err
	}
	if champion.Revision.CapabilityID != policy.Champion.CapabilityID || challenger.Revision.CapabilityID != policy.Champion.CapabilityID {
		return Comparison{}, fmt.Errorf("fitness evidence belongs to a different capability than the promotion policy")
	}
	if champion.Revision.RevisionID != policy.Champion.RevisionID {
		return Comparison{}, fmt.Errorf("champion evidence does not refer to the configured champion revision")
	}
	if challenger.Revision.RevisionID == champion.Revision.RevisionID {
		return Comparison{}, fmt.Errorf("challenger must be a different immutable revision")
	}
	if !scopeEqual(champion.Scope, policy.Scope) || !scopeEqual(challenger.Scope, policy.Scope) {
		return Comparison{}, fmt.Errorf("fitness evidence eval-suite/task-distribution scope does not exactly match promotion policy")
	}

	criterionResults := make([]CriterionResult, 0, len(policy.Criteria))
	decision := DecisionPassesGate
	for _, criterion := range policy.Criteria {
		result := evaluateCriterion(criterion, champion, challenger)
		criterionResults = append(criterionResults, result)
		if result.Status == CriterionFail {
			decision = DecisionFailsGate
		} else if result.Status == CriterionInconclusive && decision != DecisionFailsGate {
			decision = DecisionInconclusive
		}
	}
	result := ComparisonResult{Decision: decision, PolicyID: policy.ID, PolicyRevision: policy.PolicyRevision, Criteria: criterionResults}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return Comparison{}, err
	}
	identityJSON, _ := json.Marshal(struct {
		PolicyID             string `json:"policy_id"`
		ChampionEvidenceID   string `json:"champion_evidence_id"`
		ChallengerEvidenceID string `json:"challenger_evidence_id"`
	}{policy.ID, champion.ID, challenger.ID})
	id := contentID("fitcmp_", input.OrganizationID, identityJSON)
	if existing, err := s.GetComparison(ctx, input.OrganizationID, id); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Comparison{}, err
	}
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO fitness_comparisons(
		id, organization_id, capability_id, champion_revision_id, challenger_revision_id,
		champion_evidence_id, challenger_evidence_id, policy_id, result_json, actor_id, correlation_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, input.OrganizationID, policy.Champion.CapabilityID,
		champion.Revision.RevisionID, challenger.Revision.RevisionID, champion.ID, challenger.ID, policy.ID,
		string(resultJSON), input.ActorID, input.CorrelationID)
	if err != nil {
		return Comparison{}, err
	}
	return s.GetComparison(ctx, input.OrganizationID, id)
}

func (s *Store) GetComparison(ctx context.Context, organizationID, id string) (Comparison, error) {
	organizationID, id = strings.TrimSpace(organizationID), strings.TrimSpace(id)
	if organizationID == "" || id == "" {
		return Comparison{}, fmt.Errorf("organization and fitness comparison id are required")
	}
	var item Comparison
	var policyID, resultJSON string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id, organization_id, capability_id, champion_revision_id,
		challenger_revision_id, champion_evidence_id, challenger_evidence_id, policy_id, result_json,
		actor_id, correlation_id, created_at FROM fitness_comparisons WHERE organization_id=? AND id=?`, organizationID, id).Scan(
		&item.ID, &item.OrganizationID, &item.CapabilityID, &item.ChampionRevisionID, &item.ChallengerRevisionID,
		&item.ChampionEvidenceID, &item.ChallengerEvidenceID, &policyID, &resultJSON,
		&item.ActorID, &item.CorrelationID, &item.CreatedAt)
	if err != nil {
		return Comparison{}, err
	}
	if err := json.Unmarshal([]byte(resultJSON), &item.Result); err != nil {
		return Comparison{}, fmt.Errorf("decode fitness comparison result: %w", err)
	}
	if item.Result.PolicyID != policyID {
		return Comparison{}, fmt.Errorf("fitness comparison policy binding is corrupt")
	}
	return item, nil
}

func (s *Store) validateExperiments(ctx context.Context, organizationID, capabilityID string, scope EvalScope, ids []string) error {
	for _, id := range ids {
		var storedCapability, evalJSON string
		err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT capability_id, eval_suite_json FROM improvement_experiments WHERE organization_id=? AND id=?`, organizationID, id).Scan(&storedCapability, &evalJSON)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("referenced experiment %q was not found in organization", id)
			}
			return err
		}
		if storedCapability != capabilityID {
			return fmt.Errorf("referenced experiment %q belongs to a different capability", id)
		}
		var suite struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(evalJSON), &suite); err != nil {
			return fmt.Errorf("decode referenced experiment eval suite: %w", err)
		}
		if suite.ID != scope.EvalSuiteID || suite.Version != scope.EvalSuiteVersion {
			return fmt.Errorf("referenced experiment %q eval suite does not match fitness evidence scope", id)
		}
	}
	return nil
}

func evaluateCriterion(c Criterion, champion, challenger Evidence) CriterionResult {
	result := CriterionResult{MetricKind: c.MetricKind, MetricName: c.MetricName, Comparator: c.Comparator, Threshold: c.Threshold, Protected: c.Protected}
	challengerMetric, challengerOK := findMetric(challenger.Metrics, c.MetricKind, c.MetricName)
	if !challengerOK {
		result.Status = CriterionInconclusive
		result.Reason = "challenger metric is missing"
		return result
	}
	result.ChallengerValue = floatPtr(challengerMetric.Value)
	chLower, chUpper, ok := metricInterval(challengerMetric, c.MinimumConfidence)
	if !ok {
		result.Status = CriterionInconclusive
		result.Reason = "challenger uncertainty is missing or below required confidence"
		return result
	}

	needsChampion := c.Comparator == ComparatorDeltaGTE || c.Comparator == ComparatorDeltaLTE
	var champLower, champUpper float64
	if needsChampion {
		championMetric, championOK := findMetric(champion.Metrics, c.MetricKind, c.MetricName)
		if !championOK {
			result.Status = CriterionInconclusive
			result.Reason = "champion metric is missing"
			return result
		}
		result.ChampionValue = floatPtr(championMetric.Value)
		if championMetric.Unit != challengerMetric.Unit {
			result.Status = CriterionInconclusive
			result.Reason = "champion and challenger metric units differ"
			return result
		}
		var ok bool
		champLower, champUpper, ok = metricInterval(championMetric, c.MinimumConfidence)
		if !ok {
			result.Status = CriterionInconclusive
			result.Reason = "champion uncertainty is missing or below required confidence"
			return result
		}
	}

	var lower, upper float64
	switch c.Comparator {
	case ComparatorDeltaGTE, ComparatorDeltaLTE:
		lower = chLower - champUpper
		upper = chUpper - champLower
		championMetric, _ := findMetric(champion.Metrics, c.MetricKind, c.MetricName)
		result.ObservedDelta = floatPtr(challengerMetric.Value - championMetric.Value)
	case ComparatorChallengerGTE, ComparatorChallengerLTE:
		lower, upper = chLower, chUpper
	default:
		result.Status = CriterionInconclusive
		result.Reason = "unsupported comparator"
		return result
	}
	result.EvaluationLower = floatPtr(lower)
	result.EvaluationUpper = floatPtr(upper)

	switch c.Comparator {
	case ComparatorDeltaGTE, ComparatorChallengerGTE:
		if lower >= c.Threshold {
			result.Status = CriterionPass
		} else if upper < c.Threshold {
			result.Status = CriterionFail
		} else {
			result.Status = CriterionInconclusive
			result.Reason = "uncertainty interval crosses the promotion threshold"
		}
	case ComparatorDeltaLTE, ComparatorChallengerLTE:
		if upper <= c.Threshold {
			result.Status = CriterionPass
		} else if lower > c.Threshold {
			result.Status = CriterionFail
		} else {
			result.Status = CriterionInconclusive
			result.Reason = "uncertainty interval crosses the promotion threshold"
		}
	}
	return result
}

func metricInterval(metric Metric, minimumConfidence float64) (float64, float64, bool) {
	if metric.Uncertainty == nil {
		if minimumConfidence > 0 {
			return 0, 0, false
		}
		return metric.Value, metric.Value, true
	}
	if metric.Uncertainty.Confidence < minimumConfidence {
		return 0, 0, false
	}
	return metric.Uncertainty.Lower, metric.Uncertainty.Upper, true
}

func findMetric(metrics []Metric, kind, name string) (Metric, bool) {
	for _, metric := range metrics {
		if metric.Kind == kind && metric.Name == name {
			return metric, true
		}
	}
	return Metric{}, false
}

func normalizeMetrics(metrics []Metric) ([]Metric, error) {
	if len(metrics) == 0 || len(metrics) > MaxMetrics {
		return nil, fmt.Errorf("fitness evidence must include between 1 and %d metrics", MaxMetrics)
	}
	out := append([]Metric(nil), metrics...)
	seen := map[string]struct{}{}
	for i := range out {
		out[i].Kind = strings.TrimSpace(out[i].Kind)
		out[i].Name = strings.TrimSpace(out[i].Name)
		out[i].Unit = strings.TrimSpace(out[i].Unit)
		out[i].EvidenceRef = strings.TrimSpace(out[i].EvidenceRef)
		if !validMetricKind(out[i].Kind) {
			return nil, fmt.Errorf("unsupported fitness metric kind %q", out[i].Kind)
		}
		if err := validateIdentity("metric name", out[i].Name, false); err != nil {
			return nil, err
		}
		if err := validateIdentity("metric unit", out[i].Unit, false); err != nil {
			return nil, err
		}
		if !finite(out[i].Value) {
			return nil, fmt.Errorf("metric %s/%s value must be finite", out[i].Kind, out[i].Name)
		}
		if out[i].EvidenceRef != "" {
			if err := validateReference(out[i].EvidenceRef); err != nil {
				return nil, fmt.Errorf("metric evidence reference: %w", err)
			}
		}
		if out[i].Uncertainty != nil {
			u := out[i].Uncertainty
			u.Method = strings.TrimSpace(u.Method)
			if err := validateIdentity("uncertainty method", u.Method, false); err != nil {
				return nil, err
			}
			if !finite(u.Confidence) || u.Confidence <= 0 || u.Confidence > 1 || !finite(u.Lower) || !finite(u.Upper) || u.Lower > out[i].Value || u.Upper < out[i].Value || u.Lower > u.Upper {
				return nil, fmt.Errorf("metric %s/%s has invalid uncertainty bounds/confidence", out[i].Kind, out[i].Name)
			}
		}
		key := out[i].Kind + "\x00" + out[i].Name
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate fitness metric %s/%s", out[i].Kind, out[i].Name)
		}
		seen[key] = struct{}{}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func normalizeCriteria(criteria []Criterion) ([]Criterion, error) {
	if len(criteria) == 0 || len(criteria) > MaxCriteria {
		return nil, fmt.Errorf("promotion policy must include between 1 and %d criteria", MaxCriteria)
	}
	out := append([]Criterion(nil), criteria...)
	seen := map[string]struct{}{}
	for i := range out {
		out[i].MetricKind = strings.TrimSpace(out[i].MetricKind)
		out[i].MetricName = strings.TrimSpace(out[i].MetricName)
		out[i].Comparator = strings.TrimSpace(out[i].Comparator)
		if !validMetricKind(out[i].MetricKind) {
			return nil, fmt.Errorf("unsupported promotion metric kind %q", out[i].MetricKind)
		}
		if err := validateIdentity("promotion metric name", out[i].MetricName, false); err != nil {
			return nil, err
		}
		switch out[i].Comparator {
		case ComparatorDeltaGTE, ComparatorDeltaLTE, ComparatorChallengerGTE, ComparatorChallengerLTE:
		default:
			return nil, fmt.Errorf("unsupported promotion comparator %q", out[i].Comparator)
		}
		if !finite(out[i].Threshold) || !finite(out[i].MinimumConfidence) || out[i].MinimumConfidence < 0 || out[i].MinimumConfidence > 1 {
			return nil, fmt.Errorf("promotion criterion %s/%s has invalid threshold/confidence", out[i].MetricKind, out[i].MetricName)
		}
		key := out[i].MetricKind + "\x00" + out[i].MetricName + "\x00" + out[i].Comparator
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate promotion criterion %s/%s/%s", out[i].MetricKind, out[i].MetricName, out[i].Comparator)
		}
		seen[key] = struct{}{}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MetricKind != out[j].MetricKind {
			return out[i].MetricKind < out[j].MetricKind
		}
		if out[i].MetricName != out[j].MetricName {
			return out[i].MetricName < out[j].MetricName
		}
		return out[i].Comparator < out[j].Comparator
	})
	return out, nil
}

func normalizeExperimentIDs(ids []string) ([]string, error) {
	if len(ids) > MaxExperiments {
		return nil, fmt.Errorf("fitness evidence may reference at most %d experiments", MaxExperiments)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if err := validateIdentity("experiment id", id, false); err != nil {
			return nil, err
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func normalizeProvenance(items []ProvenanceReference) ([]ProvenanceReference, error) {
	if len(items) > MaxProvenance {
		return nil, fmt.Errorf("fitness evidence may include at most %d provenance references", MaxProvenance)
	}
	out := append([]ProvenanceReference(nil), items...)
	for i := range out {
		out[i].Kind = strings.TrimSpace(out[i].Kind)
		out[i].Reference = strings.TrimSpace(out[i].Reference)
		out[i].SHA256 = strings.ToLower(strings.TrimSpace(out[i].SHA256))
		if err := validateIdentity("provenance kind", out[i].Kind, false); err != nil {
			return nil, err
		}
		if err := validateReference(out[i].Reference); err != nil {
			return nil, err
		}
		if out[i].SHA256 != "" && !digestPattern.MatchString(out[i].SHA256) {
			return nil, fmt.Errorf("provenance sha256 must be 64 lowercase hex characters")
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Reference != out[j].Reference {
			return out[i].Reference < out[j].Reference
		}
		return out[i].SHA256 < out[j].SHA256
	})
	return out, nil
}

func validateScope(scope *EvalScope) error {
	scope.EvalSuiteID = strings.TrimSpace(scope.EvalSuiteID)
	scope.EvalSuiteVersion = strings.TrimSpace(scope.EvalSuiteVersion)
	scope.TaskDistributionID = strings.TrimSpace(scope.TaskDistributionID)
	scope.TaskDistributionVersion = strings.TrimSpace(scope.TaskDistributionVersion)
	for name, value := range map[string]string{
		"eval suite id": scope.EvalSuiteID, "eval suite version": scope.EvalSuiteVersion,
		"task distribution id": scope.TaskDistributionID, "task distribution version": scope.TaskDistributionVersion,
	} {
		if err := validateIdentity(name, value, false); err != nil {
			return err
		}
	}
	return nil
}

func validateExecutor(executor *ExecutionIdentity) error {
	executor.Agent = strings.TrimSpace(executor.Agent)
	executor.Model = strings.TrimSpace(executor.Model)
	executor.Harness = strings.TrimSpace(executor.Harness)
	executor.Toolchain = strings.TrimSpace(executor.Toolchain)
	if executor.Agent != "" {
		if err := validateIdentity("executor agent", executor.Agent, true); err != nil {
			return err
		}
	}
	for name, value := range map[string]string{"executor model": executor.Model, "executor harness": executor.Harness, "executor toolchain": executor.Toolchain} {
		if err := validateIdentity(name, value, false); err != nil {
			return err
		}
	}
	return nil
}

func validateRun(run *RunMetadata) error {
	run.RunID = strings.TrimSpace(run.RunID)
	run.Seed = strings.TrimSpace(run.Seed)
	run.SampleSetSHA256 = strings.ToLower(strings.TrimSpace(run.SampleSetSHA256))
	if err := validateIdentity("run id", run.RunID, false); err != nil {
		return err
	}
	if err := validateIdentity("run seed", run.Seed, false); err != nil {
		return err
	}
	if run.SampleCount < 1 {
		return fmt.Errorf("sample count must be positive")
	}
	if !digestPattern.MatchString(run.SampleSetSHA256) {
		return fmt.Errorf("sample set sha256 must be 64 lowercase hex characters")
	}
	return nil
}

func validMetricKind(kind string) bool {
	switch kind {
	case MetricQuality, MetricRegression, MetricCost, MetricRuntime, MetricResource:
		return true
	default:
		return false
	}
}

func validateIdentity(name, value string, allowEmpty bool) error {
	value = strings.TrimSpace(value)
	if value == "" && !allowEmpty {
		return fmt.Errorf("%s is required", name)
	}
	if utf8.RuneCountInString(value) > MaxIdentityBytes || len(value) > MaxIdentityBytes {
		return fmt.Errorf("%s exceeds %d bytes", name, MaxIdentityBytes)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func validateReference(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("reference is required")
	}
	if len(value) > MaxReferenceBytes || !utf8.ValidString(value) {
		return fmt.Errorf("reference exceeds %d bytes or is invalid UTF-8", MaxReferenceBytes)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("reference contains control characters")
		}
	}
	return nil
}

func scopeEqual(a, b EvalScope) bool {
	return a.EvalSuiteID == b.EvalSuiteID && a.EvalSuiteVersion == b.EvalSuiteVersion &&
		a.TaskDistributionID == b.TaskDistributionID && a.TaskDistributionVersion == b.TaskDistributionVersion
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func contentID(prefix, organizationID string, payload []byte) string {
	sum := sha256.Sum256(append([]byte(organizationID+"\x00"), payload...))
	return prefix + hex.EncodeToString(sum[:16])
}

func floatPtr(value float64) *float64 { return &value }
