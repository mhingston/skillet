// Package experiment owns opt-in, immutable improvement experiment specifications
// and bound result evidence. Skillet describes experiments but never executes them.
package experiment

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/proposal"
)

const (
	StatusIssued    = "issued"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusStale     = "stale"

	MaxHypothesisBytes = 4 * 1024
	MaxOutcomeBytes    = 2 * 1024
	MaxIdentityBytes   = 256
	MaxSummaryBytes    = 4 * 1024
	MaxReferenceBytes  = 2 * 1024
	MaxProtectedEvals  = 64
	MaxArtifacts       = 64
)

var (
	ErrStaleBase  = errors.New("experiment base revision is stale")
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Base struct {
	StableCapabilityID string `json:"stable_capability_id"`
	RevisionID         string `json:"revision_id"`
	Commit             string `json:"commit"`
	Tree               string `json:"tree"`
	ArchiveSHA256TarGZ string `json:"archive_sha256_tar_gz,omitempty"`
	ArchiveSHA256ZIP   string `json:"archive_sha256_zip,omitempty"`
	RepositoryID       string `json:"repository_id"`
	SourcePath         string `json:"source_path"`
}

type EvidenceReference struct {
	Kind              string `json:"kind"`
	ID                int64  `json:"id"`
	RevisionID        string `json:"revision_id"`
	ArchiveSHA256     string `json:"archive_sha256,omitempty"`
	MaterializationID string `json:"materialization_id,omitempty"`
	SummarySHA256     string `json:"summary_sha256,omitempty"`
}

type Origin struct {
	ProposalID             string              `json:"proposal_id"`
	CandidateID            string              `json:"candidate_id"`
	ProposalSnapshotSHA256 string              `json:"proposal_snapshot_sha256"`
	PatchSHA256            string              `json:"patch_sha256,omitempty"`
	ExternalReference      string              `json:"external_reference,omitempty"`
	Evidence               []EvidenceReference `json:"evidence"`
}

type ExecutorIdentity struct {
	Agent     string `json:"agent,omitempty"`
	Model     string `json:"model,omitempty"`
	Harness   string `json:"harness,omitempty"`
	Toolchain string `json:"toolchain,omitempty"`
}

type ProtectedEval struct {
	Name       string  `json:"name"`
	Metric     string  `json:"metric"`
	Comparator string  `json:"comparator"`
	Threshold  float64 `json:"threshold"`
}

type EvalSuite struct {
	ID        string          `json:"id"`
	Version   string          `json:"version"`
	Protected []ProtectedEval `json:"protected"`
}

type Budget struct {
	Currency          string `json:"currency,omitempty"`
	MaxCostMicrounits int64  `json:"max_cost_microunits,omitempty"`
	MaxRuntimeSeconds int64  `json:"max_runtime_seconds,omitempty"`
	MaxInputTokens    int64  `json:"max_input_tokens,omitempty"`
	MaxOutputTokens   int64  `json:"max_output_tokens,omitempty"`
}

type Spec struct {
	SchemaVersion   string           `json:"schema_version"`
	Base            Base             `json:"base"`
	Origin          Origin           `json:"origin"`
	Hypothesis      string           `json:"hypothesis"`
	IntendedOutcome string           `json:"intended_outcome"`
	Executor        ExecutorIdentity `json:"executor"`
	EvalSuite       EvalSuite        `json:"eval_suite"`
	Budget          Budget           `json:"budget"`
	Obligations     []string         `json:"obligations"`
}

type Handoff struct {
	SchemaVersion string `json:"schema_version"`
	ExperimentID  string `json:"experiment_id"`
	SpecRevision  string `json:"spec_revision"`
	Spec          Spec   `json:"spec"`
}

type Measurement struct {
	Name        string  `json:"name"`
	Value       float64 `json:"value"`
	EvidenceRef string  `json:"evidence_ref,omitempty"`
}

type EvalOutcome struct {
	Name        string  `json:"name"`
	Metric      string  `json:"metric"`
	Comparator  string  `json:"comparator"`
	Threshold   float64 `json:"threshold"`
	Value       float64 `json:"value"`
	Passed      bool    `json:"passed"`
	EvidenceRef string  `json:"evidence_ref,omitempty"`
}

type ArtifactReference struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
	SHA256    string `json:"sha256,omitempty"`
}

type Result struct {
	SpecRevision     string              `json:"spec_revision"`
	HandoffSHA256    string              `json:"handoff_sha256"`
	EvalSuiteID      string              `json:"eval_suite_id"`
	EvalSuiteVersion string              `json:"eval_suite_version"`
	Outcomes         []EvalOutcome       `json:"outcomes,omitempty"`
	Artifacts        []ArtifactReference `json:"artifacts,omitempty"`
	FailureReason    string              `json:"failure_reason,omitempty"`
	Summary          string              `json:"summary,omitempty"`
	ActorID          string              `json:"actor_id"`
	CorrelationID    string              `json:"correlation_id,omitempty"`
}

type Experiment struct {
	ID              string           `json:"id"`
	OrganizationID  string           `json:"organization_id"`
	CapabilityID    string           `json:"capability_id"`
	Base            Base             `json:"base"`
	Origin          Origin           `json:"origin"`
	Hypothesis      string           `json:"hypothesis"`
	IntendedOutcome string           `json:"intended_outcome"`
	Executor        ExecutorIdentity `json:"executor"`
	EvalSuite       EvalSuite        `json:"eval_suite"`
	Budget          Budget           `json:"budget"`
	SpecRevision    string           `json:"spec_revision"`
	Handoff         Handoff          `json:"handoff"`
	HandoffSHA256   string           `json:"handoff_sha256"`
	Result          *Result          `json:"result,omitempty"`
	ResultSHA256    string           `json:"result_sha256,omitempty"`
	Status          string           `json:"status"`
	ActorID         string           `json:"actor_id"`
	CorrelationID   string           `json:"correlation_id,omitempty"`
	CreatedAt       string           `json:"created_at"`
	UpdatedAt       string           `json:"updated_at"`
}

type CreateInput struct {
	OrganizationID  string
	ActorID         string
	CorrelationID   string
	Proposal        proposal.Proposal
	Hypothesis      string
	IntendedOutcome string
	Executor        ExecutorIdentity
	EvalSuite       EvalSuite
	Budget          Budget
}

type SubmitResultInput struct {
	OrganizationID   string
	ActorID          string
	CorrelationID    string
	ExperimentID     string
	SpecRevision     string
	HandoffSHA256    string
	EvalSuiteID      string
	EvalSuiteVersion string
	Measurements     []Measurement
	Artifacts        []ArtifactReference
	FailureReason    string
	Summary          string
}

type Store struct {
	Catalogue *catalogue.Store
}

func New(ctx context.Context, catalog *catalogue.Store) (*Store, error) {
	if catalog == nil || catalog.DB == nil {
		return nil, fmt.Errorf("experiment catalogue is required")
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS improvement_experiments (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			capability_id TEXT NOT NULL,
			base_revision_id TEXT NOT NULL,
			proposal_id TEXT NOT NULL,
			candidate_id TEXT NOT NULL,
			proposal_snapshot_sha256 TEXT NOT NULL,
			patch_sha256 TEXT NOT NULL DEFAULT '',
			external_reference TEXT NOT NULL DEFAULT '',
			evidence_json TEXT NOT NULL,
			hypothesis TEXT NOT NULL,
			intended_outcome TEXT NOT NULL,
			executor_json TEXT NOT NULL,
			eval_suite_json TEXT NOT NULL,
			budget_json TEXT NOT NULL,
			spec_revision TEXT NOT NULL,
			handoff_json TEXT NOT NULL,
			handoff_sha256 TEXT NOT NULL,
			result_json TEXT NOT NULL DEFAULT '',
			result_sha256 TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'issued' CHECK(status IN ('issued','completed','failed','cancelled','stale')),
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(organization_id, spec_revision)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_improvement_experiments_revision ON improvement_experiments(organization_id, base_revision_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_improvement_experiments_proposal ON improvement_experiments(organization_id, proposal_id, created_at, id)`,
	}
	for _, statement := range statements {
		if _, err := catalog.DB.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize experiment schema: %w", err)
		}
	}
	return &Store{Catalogue: catalog}, nil
}

func (s *Store) Create(ctx context.Context, input CreateInput) (Experiment, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.Hypothesis = strings.TrimSpace(input.Hypothesis)
	input.IntendedOutcome = strings.TrimSpace(input.IntendedOutcome)
	if input.OrganizationID == "" || input.ActorID == "" {
		return Experiment{}, fmt.Errorf("organization and actor are required")
	}
	if input.Proposal.OrganizationID != input.OrganizationID {
		return Experiment{}, fmt.Errorf("proposal organization does not match experiment organization")
	}
	if input.Proposal.Status != proposal.StatusReadyForReview {
		return Experiment{}, fmt.Errorf("experiment requires a ready-for-review proposal")
	}
	if input.Proposal.ID == "" || input.Proposal.CandidateID == "" || input.Proposal.CapabilityID == "" || input.Proposal.Base.RevisionID == "" {
		return Experiment{}, fmt.Errorf("complete proposal provenance is required")
	}
	if err := validateBoundedSecretFree("hypothesis", input.Hypothesis, MaxHypothesisBytes, true); err != nil {
		return Experiment{}, err
	}
	if input.IntendedOutcome == "" {
		input.IntendedOutcome = strings.TrimSpace(input.Proposal.IntendedOutcome)
	}
	if err := validateBoundedSecretFree("intended outcome", input.IntendedOutcome, MaxOutcomeBytes, true); err != nil {
		return Experiment{}, err
	}
	if err := validateExecutor(&input.Executor); err != nil {
		return Experiment{}, err
	}
	if err := validateEvalSuite(&input.EvalSuite); err != nil {
		return Experiment{}, err
	}
	if err := validateBudget(&input.Budget); err != nil {
		return Experiment{}, err
	}
	if err := s.requireCurrentBase(ctx, input.OrganizationID, input.Proposal.CapabilityID, input.Proposal.Base.RevisionID); err != nil {
		return Experiment{}, err
	}

	base := Base{
		StableCapabilityID: input.Proposal.CapabilityID,
		RevisionID: input.Proposal.Base.RevisionID,
		Commit: input.Proposal.Base.Commit,
		Tree: input.Proposal.Base.Tree,
		ArchiveSHA256TarGZ: input.Proposal.Base.ArchiveSHA256TarGZ,
		ArchiveSHA256ZIP: input.Proposal.Base.ArchiveSHA256ZIP,
		RepositoryID: input.Proposal.Base.RepositoryID,
		SourcePath: input.Proposal.Base.SourcePath,
	}
	origin, err := proposalOrigin(input.Proposal)
	if err != nil {
		return Experiment{}, err
	}
	spec := Spec{
		SchemaVersion:   "skillet.improvement-experiment-spec/v1",
		Base:            base,
		Origin:          origin,
		Hypothesis:      input.Hypothesis,
		IntendedOutcome: input.IntendedOutcome,
		Executor:        input.Executor,
		EvalSuite:       input.EvalSuite,
		Budget:          input.Budget,
		Obligations: []string{
			"External runners must use only the exact immutable inputs identified by this spec and must not broaden source or execution authority.",
			"Protected eval names, metrics, comparators, thresholds, and suite version are immutable for this experiment and are evaluated by Skillet on result intake.",
			"Result evidence must bind to this exact experiment id, spec revision, handoff digest, and eval suite version.",
			"No result may mutate canonical capability source, active revision, governance, or retrieval ranking; normal source review and ingestion remain authoritative.",
		},
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return Experiment{}, err
	}
	specSum := sha256.Sum256(specJSON)
	specRevision := "sha256:" + hex.EncodeToString(specSum[:])
	idSum := sha256.Sum256([]byte(input.OrganizationID + "\x00" + specRevision))
	id := "exp_" + hex.EncodeToString(idSum[:16])
	handoff := Handoff{
		SchemaVersion: "skillet.improvement-experiment-handoff/v1",
		ExperimentID:  id,
		SpecRevision:  specRevision,
		Spec:          spec,
	}
	handoffJSON, err := json.Marshal(handoff)
	if err != nil {
		return Experiment{}, err
	}
	handoffSum := sha256.Sum256(handoffJSON)
	handoffSHA := hex.EncodeToString(handoffSum[:])

	if existing, err := s.Get(ctx, input.OrganizationID, id); err == nil {
		if existing.SpecRevision != specRevision || existing.HandoffSHA256 != handoffSHA {
			return Experiment{}, fmt.Errorf("deterministic experiment identity collision")
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Experiment{}, err
	}

	evidenceJSON, _ := json.Marshal(origin.Evidence)
	executorJSON, _ := json.Marshal(input.Executor)
	evalSuiteJSON, _ := json.Marshal(input.EvalSuite)
	budgetJSON, _ := json.Marshal(input.Budget)
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO improvement_experiments(
		id, organization_id, capability_id, base_revision_id, proposal_id, candidate_id,
		proposal_snapshot_sha256, patch_sha256, external_reference, evidence_json,
		hypothesis, intended_outcome, executor_json, eval_suite_json, budget_json,
		spec_revision, handoff_json, handoff_sha256, actor_id, correlation_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.OrganizationID, input.Proposal.CapabilityID, input.Proposal.Base.RevisionID,
		origin.ProposalID, origin.CandidateID, origin.ProposalSnapshotSHA256, origin.PatchSHA256,
		origin.ExternalReference, string(evidenceJSON), input.Hypothesis, input.IntendedOutcome,
		string(executorJSON), string(evalSuiteJSON), string(budgetJSON), specRevision,
		string(handoffJSON), handoffSHA, input.ActorID, input.CorrelationID)
	if err != nil {
		return Experiment{}, err
	}
	return s.Get(ctx, input.OrganizationID, id)
}

func (s *Store) Get(ctx context.Context, organizationID, id string) (Experiment, error) {
	organizationID, id = strings.TrimSpace(organizationID), strings.TrimSpace(id)
	if organizationID == "" || id == "" {
		return Experiment{}, fmt.Errorf("organization and experiment id are required")
	}
	item, err := s.load(ctx, organizationID, id)
	if err != nil {
		return Experiment{}, err
	}
	if item.Status == StatusIssued {
		if err := s.requireCurrentBase(ctx, organizationID, item.CapabilityID, item.Base.RevisionID); errors.Is(err, ErrStaleBase) {
			if _, updateErr := s.Catalogue.DB.ExecContext(ctx, `UPDATE improvement_experiments SET status='stale', updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE organization_id=? AND id=? AND status='issued'`, organizationID, id); updateErr != nil {
				return Experiment{}, updateErr
			}
			item.Status = StatusStale
		} else if err != nil {
			return Experiment{}, err
		}
	}
	return item, nil
}

func (s *Store) ListRevision(ctx context.Context, organizationID, revisionID string, limit int) ([]Experiment, error) {
	organizationID, revisionID = strings.TrimSpace(organizationID), strings.TrimSpace(revisionID)
	if organizationID == "" || revisionID == "" {
		return nil, fmt.Errorf("organization and revision id are required")
	}
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("experiment limit must be between 1 and 100")
	}
	rows, err := s.Catalogue.DB.QueryContext(ctx, `SELECT id FROM improvement_experiments WHERE organization_id=? AND base_revision_id=? ORDER BY created_at DESC, id LIMIT ?`, organizationID, revisionID, limit)
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
	items := make([]Experiment, 0, len(ids))
	for _, id := range ids {
		item, err := s.Get(ctx, organizationID, id)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) SubmitResult(ctx context.Context, input SubmitResultInput) (Experiment, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.ExperimentID = strings.TrimSpace(input.ExperimentID)
	input.SpecRevision = strings.TrimSpace(input.SpecRevision)
	input.HandoffSHA256 = strings.TrimSpace(input.HandoffSHA256)
	input.EvalSuiteID = strings.TrimSpace(input.EvalSuiteID)
	input.EvalSuiteVersion = strings.TrimSpace(input.EvalSuiteVersion)
	input.FailureReason = strings.TrimSpace(input.FailureReason)
	input.Summary = strings.TrimSpace(input.Summary)
	if input.OrganizationID == "" || input.ActorID == "" || input.ExperimentID == "" {
		return Experiment{}, fmt.Errorf("organization, actor, and experiment id are required")
	}
	item, err := s.Get(ctx, input.OrganizationID, input.ExperimentID)
	if err != nil {
		return Experiment{}, err
	}
	if item.Status == StatusStale {
		return item, ErrStaleBase
	}
	if item.Status != StatusIssued {
		return Experiment{}, fmt.Errorf("experiment is terminal with status %s", item.Status)
	}
	if input.SpecRevision != item.SpecRevision || input.HandoffSHA256 != item.HandoffSHA256 {
		return Experiment{}, fmt.Errorf("result does not match exact experiment spec revision and handoff digest")
	}
	if input.EvalSuiteID != item.EvalSuite.ID || input.EvalSuiteVersion != item.EvalSuite.Version {
		return Experiment{}, fmt.Errorf("result eval suite identity/version does not match protected experiment spec")
	}
	if err := validateBoundedSecretFree("result summary", input.Summary, MaxSummaryBytes, false); err != nil {
		return Experiment{}, err
	}
	if err := validateBoundedSecretFree("failure reason", input.FailureReason, MaxSummaryBytes, false); err != nil {
		return Experiment{}, err
	}
	artifacts, err := validateArtifacts(input.Artifacts)
	if err != nil {
		return Experiment{}, err
	}

	status := StatusCompleted
	var outcomes []EvalOutcome
	if input.FailureReason != "" {
		if len(input.Measurements) != 0 {
			return Experiment{}, fmt.Errorf("failed experiment result must not include protected eval measurements")
		}
		status = StatusFailed
	} else {
		outcomes, err = evaluateProtected(item.EvalSuite.Protected, input.Measurements)
		if err != nil {
			return Experiment{}, err
		}
		for _, outcome := range outcomes {
			if !outcome.Passed {
				status = StatusFailed
				break
			}
		}
	}
	result := Result{
		SpecRevision:     item.SpecRevision,
		HandoffSHA256:    item.HandoffSHA256,
		EvalSuiteID:      item.EvalSuite.ID,
		EvalSuiteVersion: item.EvalSuite.Version,
		Outcomes:         outcomes,
		Artifacts:        artifacts,
		FailureReason:    input.FailureReason,
		Summary:          input.Summary,
		ActorID:          input.ActorID,
		CorrelationID:    input.CorrelationID,
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return Experiment{}, err
	}
	resultSum := sha256.Sum256(resultJSON)
	resultSHA := hex.EncodeToString(resultSum[:])
	_, err = s.Catalogue.DB.ExecContext(ctx, `UPDATE improvement_experiments SET result_json=?, result_sha256=?, status=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE organization_id=? AND id=? AND status='issued'`, string(resultJSON), resultSHA, status, input.OrganizationID, item.ID)
	if err != nil {
		return Experiment{}, err
	}
	return s.Get(ctx, input.OrganizationID, item.ID)
}

func (s *Store) Cancel(ctx context.Context, organizationID, experimentID, actorID string) (Experiment, error) {
	organizationID = strings.TrimSpace(organizationID)
	experimentID = strings.TrimSpace(experimentID)
	actorID = strings.TrimSpace(actorID)
	if organizationID == "" || experimentID == "" || actorID == "" {
		return Experiment{}, fmt.Errorf("organization, experiment, and actor are required")
	}
	item, err := s.Get(ctx, organizationID, experimentID)
	if err != nil {
		return Experiment{}, err
	}
	if item.Status != StatusIssued {
		return item, nil
	}
	_, err = s.Catalogue.DB.ExecContext(ctx, `UPDATE improvement_experiments SET status='cancelled', updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE organization_id=? AND id=? AND status='issued'`, organizationID, experimentID)
	if err != nil {
		return Experiment{}, err
	}
	return s.Get(ctx, organizationID, experimentID)
}

func (s *Store) load(ctx context.Context, organizationID, id string) (Experiment, error) {
	var item Experiment
	var evidenceJSON, executorJSON, evalSuiteJSON, budgetJSON, handoffJSON, resultJSON string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id, organization_id, capability_id, base_revision_id,
		proposal_id, candidate_id, proposal_snapshot_sha256, patch_sha256, external_reference, evidence_json,
		hypothesis, intended_outcome, executor_json, eval_suite_json, budget_json, spec_revision,
		handoff_json, handoff_sha256, result_json, result_sha256, status, actor_id, correlation_id, created_at, updated_at
		FROM improvement_experiments WHERE organization_id=? AND id=?`, organizationID, id).Scan(
		&item.ID, &item.OrganizationID, &item.CapabilityID, &item.Base.RevisionID,
		&item.Origin.ProposalID, &item.Origin.CandidateID, &item.Origin.ProposalSnapshotSHA256, &item.Origin.PatchSHA256, &item.Origin.ExternalReference, &evidenceJSON,
		&item.Hypothesis, &item.IntendedOutcome, &executorJSON, &evalSuiteJSON, &budgetJSON, &item.SpecRevision,
		&handoffJSON, &item.HandoffSHA256, &resultJSON, &item.ResultSHA256, &item.Status, &item.ActorID, &item.CorrelationID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return Experiment{}, err
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &item.Origin.Evidence); err != nil {
		return Experiment{}, fmt.Errorf("decode experiment evidence: %w", err)
	}
	if err := json.Unmarshal([]byte(executorJSON), &item.Executor); err != nil {
		return Experiment{}, fmt.Errorf("decode experiment executor: %w", err)
	}
	if err := json.Unmarshal([]byte(evalSuiteJSON), &item.EvalSuite); err != nil {
		return Experiment{}, fmt.Errorf("decode experiment eval suite: %w", err)
	}
	if err := json.Unmarshal([]byte(budgetJSON), &item.Budget); err != nil {
		return Experiment{}, fmt.Errorf("decode experiment budget: %w", err)
	}
	if err := json.Unmarshal([]byte(handoffJSON), &item.Handoff); err != nil {
		return Experiment{}, fmt.Errorf("decode experiment handoff: %w", err)
	}
	item.Base = item.Handoff.Spec.Base
	item.Origin = item.Handoff.Spec.Origin
	if resultJSON != "" {
		var result Result
		if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
			return Experiment{}, fmt.Errorf("decode experiment result: %w", err)
		}
		item.Result = &result
	}
	return item, nil
}

func (s *Store) requireCurrentBase(ctx context.Context, organizationID, capabilityID, revisionID string) error {
	var active sql.NullString
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT active_revision_id FROM skills WHERE organization_id=? AND id=?`, organizationID, capabilityID).Scan(&active)
	if err != nil {
		return err
	}
	if !active.Valid || strings.TrimSpace(active.String) == "" || active.String != revisionID {
		return ErrStaleBase
	}
	return nil
}

func proposalOrigin(item proposal.Proposal) (Origin, error) {
	if item.PatchSHA256 == "" && item.ExternalReference == "" {
		return Origin{}, fmt.Errorf("proposal has no immutable patch digest or external review reference")
	}
	external := strings.TrimSpace(item.ExternalReference)
	if external != "" {
		if err := validateSafeReference(external); err != nil {
			if item.PatchSHA256 == "" {
				return Origin{}, fmt.Errorf("proposal external reference is unsafe for experiment handoff: %w", err)
			}
			external = ""
		}
	}
	evidence := make([]EvidenceReference, 0, len(item.Evidence))
	for _, ref := range item.Evidence {
		evidence = append(evidence, EvidenceReference{
			Kind:              ref.Kind,
			ID:                ref.ID,
			RevisionID:        ref.RevisionID,
			ArchiveSHA256:     ref.ArchiveSHA256,
			MaterializationID: ref.MaterializationID,
			SummarySHA256:     ref.SummarySHA256,
		})
	}
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].Kind != evidence[j].Kind {
			return evidence[i].Kind < evidence[j].Kind
		}
		return evidence[i].ID < evidence[j].ID
	})
	snapshot := struct {
		ProposalID        string              `json:"proposal_id"`
		CandidateID       string              `json:"candidate_id"`
		BaseRevisionID    string              `json:"base_revision_id"`
		PatchSHA256       string              `json:"patch_sha256,omitempty"`
		ExternalReference string              `json:"external_reference,omitempty"`
		Evidence          []EvidenceReference `json:"evidence"`
	}{item.ID, item.CandidateID, item.Base.RevisionID, item.PatchSHA256, external, evidence}
	b, err := json.Marshal(snapshot)
	if err != nil {
		return Origin{}, err
	}
	sum := sha256.Sum256(b)
	return Origin{
		ProposalID:             item.ID,
		CandidateID:            item.CandidateID,
		ProposalSnapshotSHA256: hex.EncodeToString(sum[:]),
		PatchSHA256:            item.PatchSHA256,
		ExternalReference:      external,
		Evidence:               evidence,
	}, nil
}

func validateExecutor(identity *ExecutorIdentity) error {
	identity.Agent = strings.TrimSpace(identity.Agent)
	identity.Model = strings.TrimSpace(identity.Model)
	identity.Harness = strings.TrimSpace(identity.Harness)
	identity.Toolchain = strings.TrimSpace(identity.Toolchain)
	for name, value := range map[string]string{
		"agent identity":     identity.Agent,
		"model identity":     identity.Model,
		"harness identity":   identity.Harness,
		"toolchain identity": identity.Toolchain,
	} {
		if err := validateBoundedSecretFree(name, value, MaxIdentityBytes, false); err != nil {
			return err
		}
	}
	return nil
}

func validateEvalSuite(suite *EvalSuite) error {
	suite.ID = strings.TrimSpace(suite.ID)
	suite.Version = strings.TrimSpace(suite.Version)
	if err := validateBoundedSecretFree("eval suite id", suite.ID, MaxIdentityBytes, true); err != nil {
		return err
	}
	if err := validateBoundedSecretFree("eval suite version", suite.Version, MaxIdentityBytes, true); err != nil {
		return err
	}
	if len(suite.Protected) == 0 || len(suite.Protected) > MaxProtectedEvals {
		return fmt.Errorf("eval suite must contain between 1 and %d protected evals", MaxProtectedEvals)
	}
	seen := map[string]struct{}{}
	for i := range suite.Protected {
		eval := &suite.Protected[i]
		eval.Name = strings.TrimSpace(eval.Name)
		eval.Metric = strings.TrimSpace(eval.Metric)
		eval.Comparator = strings.ToLower(strings.TrimSpace(eval.Comparator))
		if err := validateBoundedSecretFree("protected eval name", eval.Name, MaxIdentityBytes, true); err != nil {
			return err
		}
		if err := validateBoundedSecretFree("protected eval metric", eval.Metric, MaxIdentityBytes, true); err != nil {
			return err
		}
		if eval.Comparator != "gte" && eval.Comparator != "lte" {
			return fmt.Errorf("protected eval %q comparator must be gte or lte", eval.Name)
		}
		if math.IsNaN(eval.Threshold) || math.IsInf(eval.Threshold, 0) {
			return fmt.Errorf("protected eval %q threshold must be finite", eval.Name)
		}
		if _, ok := seen[eval.Name]; ok {
			return fmt.Errorf("duplicate protected eval %q", eval.Name)
		}
		seen[eval.Name] = struct{}{}
	}
	sort.Slice(suite.Protected, func(i, j int) bool { return suite.Protected[i].Name < suite.Protected[j].Name })
	return nil
}

func validateBudget(budget *Budget) error {
	budget.Currency = strings.ToUpper(strings.TrimSpace(budget.Currency))
	if budget.MaxCostMicrounits < 0 || budget.MaxRuntimeSeconds < 0 || budget.MaxInputTokens < 0 || budget.MaxOutputTokens < 0 {
		return fmt.Errorf("experiment budgets must not be negative")
	}
	if budget.MaxCostMicrounits > 0 {
		if len(budget.Currency) != 3 {
			return fmt.Errorf("currency must be a three-letter code when a cost budget is set")
		}
		for _, r := range budget.Currency {
			if r < 'A' || r > 'Z' {
				return fmt.Errorf("currency must be a three-letter code when a cost budget is set")
			}
		}
	} else if budget.Currency != "" {
		return fmt.Errorf("currency requires max_cost_microunits")
	}
	return nil
}

func evaluateProtected(expected []ProtectedEval, measurements []Measurement) ([]EvalOutcome, error) {
	if len(measurements) != len(expected) {
		return nil, fmt.Errorf("result must contain exactly one measurement for every protected eval")
	}
	byName := make(map[string]Measurement, len(measurements))
	for _, measurement := range measurements {
		measurement.Name = strings.TrimSpace(measurement.Name)
		measurement.EvidenceRef = strings.TrimSpace(measurement.EvidenceRef)
		if measurement.Name == "" || math.IsNaN(measurement.Value) || math.IsInf(measurement.Value, 0) {
			return nil, fmt.Errorf("protected eval measurements require a name and finite value")
		}
		if measurement.EvidenceRef != "" {
			if err := validateSafeReference(measurement.EvidenceRef); err != nil {
				return nil, fmt.Errorf("measurement %q evidence reference: %w", measurement.Name, err)
			}
		}
		if _, ok := byName[measurement.Name]; ok {
			return nil, fmt.Errorf("duplicate protected eval measurement %q", measurement.Name)
		}
		byName[measurement.Name] = measurement
	}
	outcomes := make([]EvalOutcome, 0, len(expected))
	for _, protected := range expected {
		measurement, ok := byName[protected.Name]
		if !ok {
			return nil, fmt.Errorf("missing protected eval measurement %q", protected.Name)
		}
		passed := measurement.Value >= protected.Threshold
		if protected.Comparator == "lte" {
			passed = measurement.Value <= protected.Threshold
		}
		outcomes = append(outcomes, EvalOutcome{
			Name:        protected.Name,
			Metric:      protected.Metric,
			Comparator:  protected.Comparator,
			Threshold:   protected.Threshold,
			Value:       measurement.Value,
			Passed:      passed,
			EvidenceRef: measurement.EvidenceRef,
		})
	}
	return outcomes, nil
}

func validateArtifacts(input []ArtifactReference) ([]ArtifactReference, error) {
	if len(input) > MaxArtifacts {
		return nil, fmt.Errorf("result artifact count exceeds %d", MaxArtifacts)
	}
	out := append([]ArtifactReference(nil), input...)
	seen := map[string]struct{}{}
	for i := range out {
		out[i].Name = strings.TrimSpace(out[i].Name)
		out[i].Reference = strings.TrimSpace(out[i].Reference)
		out[i].SHA256 = strings.ToLower(strings.TrimSpace(out[i].SHA256))
		if err := validateBoundedSecretFree("artifact name", out[i].Name, MaxIdentityBytes, true); err != nil {
			return nil, err
		}
		if err := validateSafeReference(out[i].Reference); err != nil {
			return nil, fmt.Errorf("artifact %q reference: %w", out[i].Name, err)
		}
		if out[i].SHA256 != "" && !digestPattern.MatchString(out[i].SHA256) {
			return nil, fmt.Errorf("artifact %q sha256 must be 64 lowercase hex characters", out[i].Name)
		}
		if _, ok := seen[out[i].Name]; ok {
			return nil, fmt.Errorf("duplicate artifact %q", out[i].Name)
		}
		seen[out[i].Name] = struct{}{}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func validateSafeReference(value string) error {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || value == "" || len([]byte(value)) > MaxReferenceBytes {
		return fmt.Errorf("reference is required, bounded, and valid UTF-8")
	}
	if strings.HasPrefix(value, "sha256:") {
		if !digestPattern.MatchString(strings.TrimPrefix(value, "sha256:")) {
			return fmt.Errorf("sha256 reference must contain exactly 64 lowercase hex characters")
		}
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("reference must be credential-free https without query/fragment, or sha256:<digest>")
	}
	if containsSecret(value) {
		return fmt.Errorf("reference appears to contain secret material")
	}
	return nil
}

func validateBoundedSecretFree(name, value string, maxBytes int, required bool) error {
	if required && value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !utf8.ValidString(value) || len([]byte(value)) > maxBytes {
		return fmt.Errorf("%s exceeds bounds or is not valid UTF-8", name)
	}
	if containsSecret(value) {
		return fmt.Errorf("%s appears to contain secret material", name)
	}
	return nil
}

func containsSecret(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"authorization: bearer ",
		"-----begin private key-----",
		"-----begin rsa private key-----",
		"api_key=", "api_key:", "apikey=", "apikey:",
		"access_token=", "access_token:", "auth_token=", "auth_token:",
		"client_secret=", "client_secret:", "password=", "password:",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
