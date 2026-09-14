// Package runner defines the opt-in, harness-neutral external improvement runner
// protocol. Skillet remains a control plane: it issues immutable dispatch payloads
// and verifies signed evidence, but never executes runner workloads.
package runner

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/experiment"
)

const (
	ProtocolVersion     = "skillet.external-improvement-runner/v1"
	StatusSchemaVersion = "skillet.external-improvement-runner-status/v1"
	ResultSchemaVersion = "skillet.external-improvement-runner-result/v1"

	StateIssued    = "issued"
	StateAccepted  = "accepted"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"

	maxIdentityBytes   = 256
	maxCapabilities    = 64
	maxScopeItems      = 256
	maxResultArtifacts = 64
)

var ErrIntegrity = errors.New("external runner evidence failed integrity verification")

type Scope struct {
	CapabilityIDs    []string `json:"capability_ids"`
	MaxCostMicrounits int64   `json:"max_cost_microunits,omitempty"`
	MaxRuntimeSeconds int64   `json:"max_runtime_seconds,omitempty"`
}

type Registration struct {
	OrganizationID       string   `json:"organization_id"`
	RunnerID             string   `json:"runner_id"`
	Version              string   `json:"version"`
	Capabilities         []string `json:"capabilities"`
	AcceptedSpecVersions []string `json:"accepted_spec_versions"`
	Scope                Scope    `json:"scope"`
	PublicKey            string   `json:"public_key"`
	RegistrationSHA256   string   `json:"registration_sha256"`
	ActorID              string   `json:"actor_id"`
	CreatedAt            string   `json:"created_at"`
}

type RegisterInput struct {
	OrganizationID       string
	ActorID              string
	RunnerID             string
	Version              string
	Capabilities         []string
	AcceptedSpecVersions []string
	Scope                Scope
	PublicKey            string
}

type RunnerIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type ImmutableReference struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	SHA256    string `json:"sha256,omitempty"`
}

type ResultDelivery struct {
	Mode                 string `json:"mode"`
	StatusSchemaVersion  string `json:"status_schema_version"`
	ResultSchemaVersion  string `json:"result_schema_version"`
	Authentication       string `json:"authentication"`
}

type Dispatch struct {
	SchemaVersion       string                       `json:"schema_version"`
	RunID               string                       `json:"run_id"`
	IdempotencyKey      string                       `json:"idempotency_key"`
	Runner              RunnerIdentity               `json:"runner"`
	RegistrationSHA256 string                       `json:"registration_sha256"`
	RequiredCapabilities []string                    `json:"required_capabilities"`
	ExperimentID        string                       `json:"experiment_id"`
	SpecRevision        string                       `json:"spec_revision"`
	HandoffSHA256       string                       `json:"handoff_sha256"`
	Handoff             experiment.Handoff           `json:"handoff"`
	ImmutableInputs     []ImmutableReference         `json:"immutable_inputs"`
	Budget              experiment.Budget            `json:"budget"`
	ResultDelivery      ResultDelivery               `json:"result_delivery"`
}

type StatusEnvelope struct {
	SchemaVersion       string `json:"schema_version"`
	RunID               string `json:"run_id"`
	RunnerID            string `json:"runner_id"`
	RunnerVersion       string `json:"runner_version"`
	RegistrationSHA256 string `json:"registration_sha256"`
	ExperimentID        string `json:"experiment_id"`
	SpecRevision        string `json:"spec_revision"`
	HandoffSHA256       string `json:"handoff_sha256"`
	DispatchSHA256      string `json:"dispatch_sha256"`
	Sequence            int64  `json:"sequence"`
	State               string `json:"state"`
	Message             string `json:"message,omitempty"`
	Signature           string `json:"signature"`
}

type ResourceUsage struct {
	Currency          string `json:"currency,omitempty"`
	CostMicrounits    int64  `json:"cost_microunits,omitempty"`
	RuntimeSeconds    int64  `json:"runtime_seconds,omitempty"`
	InputTokens       int64  `json:"input_tokens,omitempty"`
	OutputTokens      int64  `json:"output_tokens,omitempty"`
}

type ResultEnvelope struct {
	SchemaVersion       string                         `json:"schema_version"`
	RunID               string                         `json:"run_id"`
	RunnerID            string                         `json:"runner_id"`
	RunnerVersion       string                         `json:"runner_version"`
	RegistrationSHA256 string                         `json:"registration_sha256"`
	ExperimentID        string                         `json:"experiment_id"`
	SpecRevision        string                         `json:"spec_revision"`
	HandoffSHA256       string                         `json:"handoff_sha256"`
	DispatchSHA256      string                         `json:"dispatch_sha256"`
	Sequence            int64                          `json:"sequence"`
	Status              string                         `json:"status"`
	Measurements        []experiment.Measurement       `json:"measurements,omitempty"`
	Artifacts           []experiment.ArtifactReference `json:"artifacts,omitempty"`
	Logs                []experiment.ArtifactReference `json:"logs,omitempty"`
	FailureReason       string                         `json:"failure_reason,omitempty"`
	Summary             string                         `json:"summary,omitempty"`
	Usage               ResourceUsage                  `json:"usage"`
	Signature           string                         `json:"signature"`
}

type BudgetAssessment struct {
	CostExceeded    bool `json:"cost_exceeded"`
	RuntimeExceeded bool `json:"runtime_exceeded"`
	InputExceeded   bool `json:"input_tokens_exceeded"`
	OutputExceeded  bool `json:"output_tokens_exceeded"`
}

type Run struct {
	ID                    string            `json:"id"`
	OrganizationID        string            `json:"organization_id"`
	ExperimentID          string            `json:"experiment_id"`
	RunnerID              string            `json:"runner_id"`
	RunnerVersion         string            `json:"runner_version"`
	RegistrationSHA256    string            `json:"registration_sha256"`
	Dispatch              Dispatch          `json:"dispatch"`
	DispatchSHA256        string            `json:"dispatch_sha256"`
	State                 string            `json:"state"`
	LastSequence          int64             `json:"last_sequence"`
	Result                *ResultEnvelope   `json:"result,omitempty"`
	ResultSHA256          string            `json:"result_sha256,omitempty"`
	BudgetAssessment      *BudgetAssessment `json:"budget_assessment,omitempty"`
	ActorID               string            `json:"actor_id"`
	CorrelationID         string            `json:"correlation_id,omitempty"`
	CreatedAt             string            `json:"created_at"`
	UpdatedAt             string            `json:"updated_at"`
}

type IssueInput struct {
	OrganizationID       string
	ActorID              string
	CorrelationID        string
	ExperimentID         string
	RunnerID             string
	RunnerVersion        string
	RequiredCapabilities []string
}

type Store struct {
	Catalogue   *catalogue.Store
	Experiments *experiment.Store
}

func New(ctx context.Context, catalog *catalogue.Store, experiments *experiment.Store) (*Store, error) {
	if catalog == nil || catalog.DB == nil || experiments == nil {
		return nil, fmt.Errorf("runner catalogue and experiment store are required")
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS external_runner_registrations (
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			runner_id TEXT NOT NULL,
			version TEXT NOT NULL,
			capabilities_json TEXT NOT NULL,
			accepted_spec_versions_json TEXT NOT NULL,
			scope_json TEXT NOT NULL,
			public_key TEXT NOT NULL,
			registration_sha256 TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(organization_id, runner_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS external_runner_runs (
			run_id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			experiment_id TEXT NOT NULL,
			runner_id TEXT NOT NULL,
			runner_version TEXT NOT NULL,
			registration_sha256 TEXT NOT NULL,
			dispatch_json TEXT NOT NULL,
			dispatch_sha256 TEXT NOT NULL,
			state TEXT NOT NULL,
			last_sequence INTEGER NOT NULL DEFAULT 0,
			result_json TEXT NOT NULL DEFAULT '',
			result_sha256 TEXT NOT NULL DEFAULT '',
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(organization_id, experiment_id, runner_id, runner_version)
		)`,
		`CREATE TABLE IF NOT EXISTS external_runner_events (
			run_id TEXT NOT NULL REFERENCES external_runner_runs(run_id),
			sequence INTEGER NOT NULL,
			kind TEXT NOT NULL CHECK(kind IN ('status','result')),
			payload_sha256 TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY(run_id, sequence)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_external_runner_runs_experiment ON external_runner_runs(organization_id, experiment_id, created_at, run_id)`,
	}
	for _, statement := range statements {
		if _, err := catalog.DB.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize external runner schema: %w", err)
		}
	}
	return &Store{Catalogue: catalog, Experiments: experiments}, nil
}

func (s *Store) Register(ctx context.Context, input RegisterInput) (Registration, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.RunnerID = strings.TrimSpace(input.RunnerID)
	input.Version = strings.TrimSpace(input.Version)
	input.PublicKey = strings.TrimSpace(input.PublicKey)
	if input.OrganizationID == "" || input.ActorID == "" {
		return Registration{}, fmt.Errorf("organization and actor are required")
	}
	if err := validateIdentity("runner id", input.RunnerID); err != nil {
		return Registration{}, err
	}
	if err := validateIdentity("runner version", input.Version); err != nil {
		return Registration{}, err
	}
	caps, err := normalizedStrings(input.Capabilities, maxCapabilities, "runner capabilities", true)
	if err != nil {
		return Registration{}, err
	}
	versions, err := normalizedStrings(input.AcceptedSpecVersions, maxCapabilities, "accepted spec versions", true)
	if err != nil {
		return Registration{}, err
	}
	if !contains(versions, "skillet.improvement-experiment-spec/v1") {
		return Registration{}, fmt.Errorf("runner must explicitly accept skillet.improvement-experiment-spec/v1")
	}
	scopeIDs, err := normalizedStrings(input.Scope.CapabilityIDs, maxScopeItems, "runner capability scope", true)
	if err != nil {
		return Registration{}, err
	}
	if input.Scope.MaxCostMicrounits < 0 || input.Scope.MaxRuntimeSeconds < 0 {
		return Registration{}, fmt.Errorf("runner scope budgets cannot be negative")
	}
	input.Scope.CapabilityIDs = scopeIDs
	key, err := base64.StdEncoding.DecodeString(input.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return Registration{}, fmt.Errorf("runner public_key must be a base64 Ed25519 public key")
	}

	spec := struct {
		OrganizationID       string   `json:"organization_id"`
		RunnerID             string   `json:"runner_id"`
		Version              string   `json:"version"`
		Capabilities         []string `json:"capabilities"`
		AcceptedSpecVersions []string `json:"accepted_spec_versions"`
		Scope                Scope    `json:"scope"`
		PublicKey            string   `json:"public_key"`
	}{input.OrganizationID, input.RunnerID, input.Version, caps, versions, input.Scope, input.PublicKey}
	registrationSHA, _, err := digestJSON(spec)
	if err != nil {
		return Registration{}, err
	}
	if existing, err := s.GetRegistration(ctx, input.OrganizationID, input.RunnerID, input.Version); err == nil {
		if existing.RegistrationSHA256 != registrationSHA {
			return Registration{}, fmt.Errorf("runner identity/version is immutable; register a new version for changed configuration")
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Registration{}, err
	}

	capsJSON, _ := json.Marshal(caps)
	versionsJSON, _ := json.Marshal(versions)
	scopeJSON, _ := json.Marshal(input.Scope)
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO external_runner_registrations(
		organization_id, runner_id, version, capabilities_json, accepted_spec_versions_json,
		scope_json, public_key, registration_sha256, actor_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, input.OrganizationID, input.RunnerID, input.Version,
		string(capsJSON), string(versionsJSON), string(scopeJSON), input.PublicKey, registrationSHA, input.ActorID)
	if err != nil {
		return Registration{}, err
	}
	return s.GetRegistration(ctx, input.OrganizationID, input.RunnerID, input.Version)
}

func (s *Store) GetRegistration(ctx context.Context, organizationID, runnerID, version string) (Registration, error) {
	var item Registration
	var capsJSON, versionsJSON, scopeJSON string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT organization_id, runner_id, version, capabilities_json,
		accepted_spec_versions_json, scope_json, public_key, registration_sha256, actor_id, created_at
		FROM external_runner_registrations WHERE organization_id=? AND runner_id=? AND version=?`,
		strings.TrimSpace(organizationID), strings.TrimSpace(runnerID), strings.TrimSpace(version)).Scan(
		&item.OrganizationID, &item.RunnerID, &item.Version, &capsJSON, &versionsJSON, &scopeJSON,
		&item.PublicKey, &item.RegistrationSHA256, &item.ActorID, &item.CreatedAt)
	if err != nil {
		return Registration{}, err
	}
	if err := json.Unmarshal([]byte(capsJSON), &item.Capabilities); err != nil {
		return Registration{}, err
	}
	if err := json.Unmarshal([]byte(versionsJSON), &item.AcceptedSpecVersions); err != nil {
		return Registration{}, err
	}
	if err := json.Unmarshal([]byte(scopeJSON), &item.Scope); err != nil {
		return Registration{}, err
	}
	return item, nil
}

func (s *Store) Issue(ctx context.Context, input IssueInput) (Run, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.ExperimentID = strings.TrimSpace(input.ExperimentID)
	input.RunnerID = strings.TrimSpace(input.RunnerID)
	input.RunnerVersion = strings.TrimSpace(input.RunnerVersion)
	if input.OrganizationID == "" || input.ActorID == "" || input.ExperimentID == "" {
		return Run{}, fmt.Errorf("organization, actor, and experiment are required")
	}
	required, err := normalizedStrings(input.RequiredCapabilities, maxCapabilities, "required runner capabilities", true)
	if err != nil {
		return Run{}, err
	}
	registration, err := s.GetRegistration(ctx, input.OrganizationID, input.RunnerID, input.RunnerVersion)
	if err != nil {
		return Run{}, err
	}
	for _, capability := range required {
		if !contains(registration.Capabilities, capability) {
			return Run{}, fmt.Errorf("runner does not declare required capability %q", capability)
		}
	}
	exp, err := s.Experiments.Get(ctx, input.OrganizationID, input.ExperimentID)
	if err != nil {
		return Run{}, err
	}
	if exp.Status != experiment.StatusIssued {
		return Run{}, fmt.Errorf("external dispatch requires an issued experiment, got %s", exp.Status)
	}
	if !contains(registration.AcceptedSpecVersions, exp.Handoff.Spec.SchemaVersion) {
		return Run{}, fmt.Errorf("runner does not accept experiment spec version %q", exp.Handoff.Spec.SchemaVersion)
	}
	if !contains(registration.Scope.CapabilityIDs, exp.CapabilityID) {
		return Run{}, fmt.Errorf("runner is not scoped to capability %q", exp.CapabilityID)
	}
	if registration.Scope.MaxCostMicrounits > 0 && exp.Budget.MaxCostMicrounits > registration.Scope.MaxCostMicrounits {
		return Run{}, fmt.Errorf("experiment cost budget exceeds runner authorization scope")
	}
	if registration.Scope.MaxRuntimeSeconds > 0 && exp.Budget.MaxRuntimeSeconds > registration.Scope.MaxRuntimeSeconds {
		return Run{}, fmt.Errorf("experiment runtime budget exceeds runner authorization scope")
	}

	idMaterial := strings.Join([]string{input.OrganizationID, exp.ID, registration.RegistrationSHA256, strings.Join(required, "\x1f")}, "\x00")
	idSum := sha256.Sum256([]byte(idMaterial))
	runID := "run_" + hex.EncodeToString(idSum[:16])
	dispatch := Dispatch{
		SchemaVersion: ProtocolVersion,
		RunID: runID,
		IdempotencyKey: "sha256:" + hex.EncodeToString(idSum[:]),
		Runner: RunnerIdentity{ID: registration.RunnerID, Version: registration.Version},
		RegistrationSHA256: registration.RegistrationSHA256,
		RequiredCapabilities: required,
		ExperimentID: exp.ID,
		SpecRevision: exp.SpecRevision,
		HandoffSHA256: exp.HandoffSHA256,
		Handoff: exp.Handoff,
		ImmutableInputs: immutableReferences(exp),
		Budget: exp.Budget,
		ResultDelivery: ResultDelivery{
			Mode: "explicit_polling_or_submission",
			StatusSchemaVersion: StatusSchemaVersion,
			ResultSchemaVersion: ResultSchemaVersion,
			Authentication: "ed25519-signature-over-canonical-json-with-signature-omitted",
		},
	}
	dispatchSHA, dispatchJSON, err := digestJSON(dispatch)
	if err != nil {
		return Run{}, err
	}
	if existing, err := s.GetRun(ctx, input.OrganizationID, runID); err == nil {
		if existing.DispatchSHA256 != dispatchSHA {
			return Run{}, fmt.Errorf("deterministic runner dispatch collision")
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Run{}, err
	}
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO external_runner_runs(
		run_id, organization_id, experiment_id, runner_id, runner_version, registration_sha256,
		dispatch_json, dispatch_sha256, state, actor_id, correlation_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, runID, input.OrganizationID, exp.ID,
		registration.RunnerID, registration.Version, registration.RegistrationSHA256, string(dispatchJSON), dispatchSHA,
		StateIssued, input.ActorID, input.CorrelationID)
	if err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, input.OrganizationID, runID)
}

func (s *Store) GetRun(ctx context.Context, organizationID, runID string) (Run, error) {
	var item Run
	var dispatchJSON, resultJSON string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT run_id, organization_id, experiment_id, runner_id,
		runner_version, registration_sha256, dispatch_json, dispatch_sha256, state, last_sequence,
		result_json, result_sha256, actor_id, correlation_id, created_at, updated_at
		FROM external_runner_runs WHERE organization_id=? AND run_id=?`, strings.TrimSpace(organizationID), strings.TrimSpace(runID)).Scan(
		&item.ID, &item.OrganizationID, &item.ExperimentID, &item.RunnerID, &item.RunnerVersion,
		&item.RegistrationSHA256, &dispatchJSON, &item.DispatchSHA256, &item.State, &item.LastSequence,
		&resultJSON, &item.ResultSHA256, &item.ActorID, &item.CorrelationID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return Run{}, err
	}
	if err := json.Unmarshal([]byte(dispatchJSON), &item.Dispatch); err != nil {
		return Run{}, fmt.Errorf("decode runner dispatch: %w", err)
	}
	if resultJSON != "" {
		var result ResultEnvelope
		if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
			return Run{}, fmt.Errorf("decode runner result: %w", err)
		}
		item.Result = &result
		assessment := assessBudget(item.Dispatch.Budget, result.Usage)
		item.BudgetAssessment = &assessment
	}
	return item, nil
}

func (s *Store) RecordStatus(ctx context.Context, organizationID string, event StatusEnvelope) (Run, error) {
	run, registration, err := s.boundRun(ctx, organizationID, event.RunID, event.RunnerID, event.RunnerVersion)
	if err != nil {
		return Run{}, err
	}
	if event.SchemaVersion != StatusSchemaVersion {
		return Run{}, fmt.Errorf("unsupported runner status schema %q", event.SchemaVersion)
	}
	if event.State != StateAccepted && event.State != StateRunning {
		return Run{}, fmt.Errorf("non-terminal status must be accepted or running")
	}
	if err := validateBoundEnvelope(run, event.RegistrationSHA256, event.ExperimentID, event.SpecRevision, event.HandoffSHA256, event.DispatchSHA256); err != nil {
		return Run{}, err
	}
	if err := verifyStatusSignature(registration, event); err != nil {
		return Run{}, err
	}
	payloadSHA, payloadJSON, err := digestJSON(event)
	if err != nil {
		return Run{}, err
	}
	if event.Sequence <= run.LastSequence {
		return s.replayOrReject(ctx, run, event.Sequence, payloadSHA)
	}
	if event.Sequence != run.LastSequence+1 {
		return Run{}, fmt.Errorf("runner status sequence must advance exactly once")
	}
	if isTerminal(run.State) {
		return Run{}, fmt.Errorf("runner run is terminal with status %s", run.State)
	}
	if _, err := s.Catalogue.DB.ExecContext(ctx, `INSERT INTO external_runner_events(run_id, sequence, kind, payload_sha256, payload_json) VALUES (?, ?, 'status', ?, ?)`, run.ID, event.Sequence, payloadSHA, string(payloadJSON)); err != nil {
		return Run{}, err
	}
	if _, err := s.Catalogue.DB.ExecContext(ctx, `UPDATE external_runner_runs SET state=?, last_sequence=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE run_id=?`, event.State, event.Sequence, run.ID); err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, organizationID, run.ID)
}

func (s *Store) SubmitResult(ctx context.Context, organizationID, actorID string, result ResultEnvelope) (Run, experiment.Experiment, error) {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return Run{}, experiment.Experiment{}, fmt.Errorf("actor is required")
	}
	run, registration, err := s.boundRun(ctx, organizationID, result.RunID, result.RunnerID, result.RunnerVersion)
	if err != nil {
		return Run{}, experiment.Experiment{}, err
	}
	if result.SchemaVersion != ResultSchemaVersion {
		return Run{}, experiment.Experiment{}, fmt.Errorf("unsupported runner result schema %q", result.SchemaVersion)
	}
	if result.Status != StateSucceeded && result.Status != StateFailed && result.Status != StateCancelled {
		return Run{}, experiment.Experiment{}, fmt.Errorf("runner result must be succeeded, failed, or cancelled")
	}
	if len(result.Artifacts) > maxResultArtifacts || len(result.Logs) > maxResultArtifacts {
		return Run{}, experiment.Experiment{}, fmt.Errorf("runner result contains too many artifact/log references")
	}
	if err := validateUsage(result.Usage); err != nil {
		return Run{}, experiment.Experiment{}, err
	}
	if err := validateBoundEnvelope(run, result.RegistrationSHA256, result.ExperimentID, result.SpecRevision, result.HandoffSHA256, result.DispatchSHA256); err != nil {
		return Run{}, experiment.Experiment{}, err
	}
	if err := verifyResultSignature(registration, result); err != nil {
		return Run{}, experiment.Experiment{}, err
	}
	payloadSHA, payloadJSON, err := digestJSON(result)
	if err != nil {
		return Run{}, experiment.Experiment{}, err
	}
	if run.ResultSHA256 != "" {
		if run.ResultSHA256 == payloadSHA {
			exp, getErr := s.Experiments.Get(ctx, organizationID, run.ExperimentID)
			return run, exp, getErr
		}
		return Run{}, experiment.Experiment{}, fmt.Errorf("runner result already terminal with different evidence")
	}
	if result.Sequence != run.LastSequence+1 {
		return Run{}, experiment.Experiment{}, fmt.Errorf("runner result sequence must advance exactly once")
	}
	if isTerminal(run.State) {
		return Run{}, experiment.Experiment{}, fmt.Errorf("runner run is terminal with status %s", run.State)
	}

	var exp experiment.Experiment
	switch result.Status {
	case StateCancelled:
		if len(result.Measurements) != 0 {
			return Run{}, experiment.Experiment{}, fmt.Errorf("cancelled runner result must not contain protected measurements")
		}
		exp, err = s.Experiments.Cancel(ctx, organizationID, run.ExperimentID, "runner:"+result.RunnerID+"@"+result.RunnerVersion)
	case StateFailed:
		if strings.TrimSpace(result.FailureReason) == "" {
			return Run{}, experiment.Experiment{}, fmt.Errorf("failed runner result requires failure_reason")
		}
		exp, err = s.Experiments.SubmitResult(ctx, experiment.SubmitResultInput{
			OrganizationID: organizationID, ActorID: actorID, CorrelationID: run.CorrelationID,
			ExperimentID: run.ExperimentID, SpecRevision: run.Dispatch.SpecRevision, HandoffSHA256: run.Dispatch.HandoffSHA256,
			EvalSuiteID: run.Dispatch.Handoff.Spec.EvalSuite.ID, EvalSuiteVersion: run.Dispatch.Handoff.Spec.EvalSuite.Version,
			Artifacts: append(append([]experiment.ArtifactReference{}, result.Artifacts...), result.Logs...),
			FailureReason: result.FailureReason, Summary: result.Summary,
		})
	case StateSucceeded:
		if strings.TrimSpace(result.FailureReason) != "" {
			return Run{}, experiment.Experiment{}, fmt.Errorf("successful runner result cannot contain failure_reason")
		}
		exp, err = s.Experiments.SubmitResult(ctx, experiment.SubmitResultInput{
			OrganizationID: organizationID, ActorID: actorID, CorrelationID: run.CorrelationID,
			ExperimentID: run.ExperimentID, SpecRevision: run.Dispatch.SpecRevision, HandoffSHA256: run.Dispatch.HandoffSHA256,
			EvalSuiteID: run.Dispatch.Handoff.Spec.EvalSuite.ID, EvalSuiteVersion: run.Dispatch.Handoff.Spec.EvalSuite.Version,
			Measurements: result.Measurements,
			Artifacts: append(append([]experiment.ArtifactReference{}, result.Artifacts...), result.Logs...),
			Summary: result.Summary,
		})
	}
	if err != nil {
		return Run{}, experiment.Experiment{}, err
	}
	if _, err := s.Catalogue.DB.ExecContext(ctx, `INSERT INTO external_runner_events(run_id, sequence, kind, payload_sha256, payload_json) VALUES (?, ?, 'result', ?, ?)`, run.ID, result.Sequence, payloadSHA, string(payloadJSON)); err != nil {
		return Run{}, experiment.Experiment{}, err
	}
	if _, err := s.Catalogue.DB.ExecContext(ctx, `UPDATE external_runner_runs SET state=?, last_sequence=?, result_json=?, result_sha256=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE run_id=?`, result.Status, result.Sequence, string(payloadJSON), payloadSHA, run.ID); err != nil {
		return Run{}, experiment.Experiment{}, err
	}
	updated, err := s.GetRun(ctx, organizationID, run.ID)
	return updated, exp, err
}

func SignStatus(privateKey ed25519.PrivateKey, event StatusEnvelope) (StatusEnvelope, error) {
	event.Signature = ""
	payload, err := json.Marshal(event)
	if err != nil {
		return StatusEnvelope{}, err
	}
	event.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return event, nil
}

func SignResult(privateKey ed25519.PrivateKey, result ResultEnvelope) (ResultEnvelope, error) {
	result.Signature = ""
	payload, err := json.Marshal(result)
	if err != nil {
		return ResultEnvelope{}, err
	}
	result.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return result, nil
}

func (s *Store) boundRun(ctx context.Context, organizationID, runID, runnerID, runnerVersion string) (Run, Registration, error) {
	run, err := s.GetRun(ctx, strings.TrimSpace(organizationID), strings.TrimSpace(runID))
	if err != nil {
		return Run{}, Registration{}, err
	}
	if strings.TrimSpace(runnerID) != run.RunnerID || strings.TrimSpace(runnerVersion) != run.RunnerVersion {
		return Run{}, Registration{}, ErrIntegrity
	}
	registration, err := s.GetRegistration(ctx, run.OrganizationID, run.RunnerID, run.RunnerVersion)
	if err != nil {
		return Run{}, Registration{}, err
	}
	if registration.RegistrationSHA256 != run.RegistrationSHA256 {
		return Run{}, Registration{}, ErrIntegrity
	}
	return run, registration, nil
}

func validateBoundEnvelope(run Run, registrationSHA, experimentID, specRevision, handoffSHA, dispatchSHA string) error {
	if registrationSHA != run.RegistrationSHA256 || experimentID != run.ExperimentID || specRevision != run.Dispatch.SpecRevision || handoffSHA != run.Dispatch.HandoffSHA256 || dispatchSHA != run.DispatchSHA256 {
		return ErrIntegrity
	}
	return nil
}

func verifyStatusSignature(reg Registration, event StatusEnvelope) error {
	signature, err := base64.StdEncoding.DecodeString(event.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ErrIntegrity
	}
	key, err := base64.StdEncoding.DecodeString(reg.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return ErrIntegrity
	}
	event.Signature = ""
	payload, err := json.Marshal(event)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(key), payload, signature) {
		return ErrIntegrity
	}
	return nil
}

func verifyResultSignature(reg Registration, result ResultEnvelope) error {
	signature, err := base64.StdEncoding.DecodeString(result.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ErrIntegrity
	}
	key, err := base64.StdEncoding.DecodeString(reg.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return ErrIntegrity
	}
	result.Signature = ""
	payload, err := json.Marshal(result)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(key), payload, signature) {
		return ErrIntegrity
	}
	return nil
}

func (s *Store) replayOrReject(ctx context.Context, run Run, sequence int64, payloadSHA string) (Run, error) {
	var existing string
	if err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT payload_sha256 FROM external_runner_events WHERE run_id=? AND sequence=?`, run.ID, sequence).Scan(&existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, fmt.Errorf("runner evidence sequence is stale or out of order")
		}
		return Run{}, err
	}
	if existing != payloadSHA {
		return Run{}, fmt.Errorf("runner replay conflicts with previously accepted evidence")
	}
	return run, nil
}

func immutableReferences(exp experiment.Experiment) []ImmutableReference {
	refs := []ImmutableReference{
		{Kind: "capability_revision", Reference: exp.Base.RevisionID, SHA256: firstDigest(exp.Base.ArchiveSHA256TarGZ, exp.Base.ArchiveSHA256ZIP)},
		{Kind: "proposal_snapshot", Reference: exp.Origin.ProposalID, SHA256: exp.Origin.ProposalSnapshotSHA256},
	}
	if exp.Origin.PatchSHA256 != "" {
		refs = append(refs, ImmutableReference{Kind: "proposal_patch", Reference: exp.Origin.ProposalID + ":patch", SHA256: exp.Origin.PatchSHA256})
	}
	for _, evidence := range exp.Origin.Evidence {
		digest := firstDigest(evidence.ArchiveSHA256, evidence.SummarySHA256)
		refs = append(refs, ImmutableReference{Kind: "evidence:" + evidence.Kind, Reference: fmt.Sprintf("%d", evidence.ID), SHA256: digest})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind == refs[j].Kind {
			return refs[i].Reference < refs[j].Reference
		}
		return refs[i].Kind < refs[j].Kind
	})
	return refs
}

func assessBudget(budget experiment.Budget, usage ResourceUsage) BudgetAssessment {
	return BudgetAssessment{
		CostExceeded: budget.MaxCostMicrounits > 0 && usage.CostMicrounits > budget.MaxCostMicrounits,
		RuntimeExceeded: budget.MaxRuntimeSeconds > 0 && usage.RuntimeSeconds > budget.MaxRuntimeSeconds,
		InputExceeded: budget.MaxInputTokens > 0 && usage.InputTokens > budget.MaxInputTokens,
		OutputExceeded: budget.MaxOutputTokens > 0 && usage.OutputTokens > budget.MaxOutputTokens,
	}
}

func validateUsage(usage ResourceUsage) error {
	if usage.CostMicrounits < 0 || usage.RuntimeSeconds < 0 || usage.InputTokens < 0 || usage.OutputTokens < 0 {
		return fmt.Errorf("runner resource usage cannot be negative")
	}
	if len(usage.Currency) > 32 {
		return fmt.Errorf("runner usage currency is too long")
	}
	return nil
}

func validateIdentity(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxIdentityBytes {
		return fmt.Errorf("%s is required and must be at most %d bytes", name, maxIdentityBytes)
	}
	return nil
}

func normalizedStrings(values []string, limit int, name string, require bool) ([]string, error) {
	if len(values) > limit {
		return nil, fmt.Errorf("%s exceeds limit %d", name, limit)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxIdentityBytes {
			return nil, fmt.Errorf("%s contains an invalid value", name)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	if require && len(out) == 0 {
		return nil, fmt.Errorf("%s requires at least one value", name)
	}
	return out, nil
}

func digestJSON(value any) (string, []byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), payload, nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func firstDigest(values ...string) string {
	for _, value := range values {
		value = strings.TrimPrefix(strings.TrimSpace(value), "sha256:")
		if len(value) == 64 {
			return value
		}
	}
	return ""
}

func isTerminal(state string) bool {
	return state == StateSucceeded || state == StateFailed || state == StateCancelled
}
