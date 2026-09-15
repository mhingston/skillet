// Package runnerfixture is a deterministic non-training adapter fixture used to
// prove the external runner protocol. It deliberately has no dependency on the
// Skillet HTTP server, database, shell, or repository mutation APIs: callers
// hand it a dispatch exactly as an external CI/repository agent would receive.
package runnerfixture

import (
	"crypto/ed25519"
	"fmt"
	"sort"

	"github.com/mhingston/skillet/internal/experiment"
	"github.com/mhingston/skillet/internal/runner"
)

type ValidationMeasurement struct {
	Name        string
	Value       float64
	EvidenceRef string
}

type DeterministicCI struct {
	PrivateKey ed25519.PrivateKey
}

func (a DeterministicCI) Accepted(dispatch runner.Dispatch, dispatchSHA256 string) (runner.StatusEnvelope, error) {
	if err := validateDispatch(dispatch); err != nil {
		return runner.StatusEnvelope{}, err
	}
	return runner.SignStatus(a.PrivateKey, runner.StatusEnvelope{
		SchemaVersion: runner.StatusSchemaVersion,
		RunID: dispatch.RunID,
		RunnerID: dispatch.Runner.ID,
		RunnerVersion: dispatch.Runner.Version,
		RegistrationSHA256: dispatch.RegistrationSHA256,
		ExperimentID: dispatch.ExperimentID,
		SpecRevision: dispatch.SpecRevision,
		HandoffSHA256: dispatch.HandoffSHA256,
		DispatchSHA256: dispatchSHA256,
		Sequence: 1,
		State: runner.StateAccepted,
		Message: "deterministic CI fixture accepted bounded validation",
	})
}

func (a DeterministicCI) Running(dispatch runner.Dispatch, dispatchSHA256 string) (runner.StatusEnvelope, error) {
	if err := validateDispatch(dispatch); err != nil {
		return runner.StatusEnvelope{}, err
	}
	return runner.SignStatus(a.PrivateKey, runner.StatusEnvelope{
		SchemaVersion: runner.StatusSchemaVersion,
		RunID: dispatch.RunID,
		RunnerID: dispatch.Runner.ID,
		RunnerVersion: dispatch.Runner.Version,
		RegistrationSHA256: dispatch.RegistrationSHA256,
		ExperimentID: dispatch.ExperimentID,
		SpecRevision: dispatch.SpecRevision,
		HandoffSHA256: dispatch.HandoffSHA256,
		DispatchSHA256: dispatchSHA256,
		Sequence: 2,
		State: runner.StateRunning,
		Message: "deterministic CI fixture is evaluating supplied bounded checks",
	})
}

func (a DeterministicCI) Complete(dispatch runner.Dispatch, dispatchSHA256 string, measurements []ValidationMeasurement, artifacts, logs []experiment.ArtifactReference, usage runner.ResourceUsage) (runner.ResultEnvelope, error) {
	if err := validateDispatch(dispatch); err != nil {
		return runner.ResultEnvelope{}, err
	}
	byName := make(map[string]ValidationMeasurement, len(measurements))
	for _, measurement := range measurements {
		if measurement.Name == "" {
			return runner.ResultEnvelope{}, fmt.Errorf("fixture measurement name is required")
		}
		if _, exists := byName[measurement.Name]; exists {
			return runner.ResultEnvelope{}, fmt.Errorf("duplicate fixture measurement %q", measurement.Name)
		}
		byName[measurement.Name] = measurement
	}
	protected := append([]experiment.ProtectedEval(nil), dispatch.Handoff.Spec.EvalSuite.Protected...)
	sort.Slice(protected, func(i, j int) bool { return protected[i].Name < protected[j].Name })
	out := make([]experiment.Measurement, 0, len(protected))
	for _, required := range protected {
		measurement, ok := byName[required.Name]
		if !ok {
			return runner.ResultEnvelope{}, fmt.Errorf("fixture is missing protected measurement %q", required.Name)
		}
		out = append(out, experiment.Measurement{Name: required.Name, Value: measurement.Value, EvidenceRef: measurement.EvidenceRef})
	}
	return runner.SignResult(a.PrivateKey, runner.ResultEnvelope{
		SchemaVersion: runner.ResultSchemaVersion,
		RunID: dispatch.RunID,
		RunnerID: dispatch.Runner.ID,
		RunnerVersion: dispatch.Runner.Version,
		RegistrationSHA256: dispatch.RegistrationSHA256,
		ExperimentID: dispatch.ExperimentID,
		SpecRevision: dispatch.SpecRevision,
		HandoffSHA256: dispatch.HandoffSHA256,
		DispatchSHA256: dispatchSHA256,
		Sequence: 3,
		Status: runner.StateSucceeded,
		Measurements: out,
		Artifacts: artifacts,
		Logs: logs,
		Summary: "deterministic external CI validation completed",
		Usage: usage,
	})
}

func (a DeterministicCI) Failed(dispatch runner.Dispatch, dispatchSHA256, reason string, usage runner.ResourceUsage) (runner.ResultEnvelope, error) {
	if err := validateDispatch(dispatch); err != nil {
		return runner.ResultEnvelope{}, err
	}
	if reason == "" {
		return runner.ResultEnvelope{}, fmt.Errorf("failure reason is required")
	}
	return runner.SignResult(a.PrivateKey, runner.ResultEnvelope{
		SchemaVersion: runner.ResultSchemaVersion,
		RunID: dispatch.RunID,
		RunnerID: dispatch.Runner.ID,
		RunnerVersion: dispatch.Runner.Version,
		RegistrationSHA256: dispatch.RegistrationSHA256,
		ExperimentID: dispatch.ExperimentID,
		SpecRevision: dispatch.SpecRevision,
		HandoffSHA256: dispatch.HandoffSHA256,
		DispatchSHA256: dispatchSHA256,
		Sequence: 3,
		Status: runner.StateFailed,
		FailureReason: reason,
		Summary: "deterministic external CI validation failed",
		Usage: usage,
	})
}

func (a DeterministicCI) Cancelled(dispatch runner.Dispatch, dispatchSHA256 string, usage runner.ResourceUsage) (runner.ResultEnvelope, error) {
	if err := validateDispatch(dispatch); err != nil {
		return runner.ResultEnvelope{}, err
	}
	return runner.SignResult(a.PrivateKey, runner.ResultEnvelope{
		SchemaVersion: runner.ResultSchemaVersion,
		RunID: dispatch.RunID,
		RunnerID: dispatch.Runner.ID,
		RunnerVersion: dispatch.Runner.Version,
		RegistrationSHA256: dispatch.RegistrationSHA256,
		ExperimentID: dispatch.ExperimentID,
		SpecRevision: dispatch.SpecRevision,
		HandoffSHA256: dispatch.HandoffSHA256,
		DispatchSHA256: dispatchSHA256,
		Sequence: 3,
		Status: runner.StateCancelled,
		Summary: "deterministic external CI validation was cancelled",
		Usage: usage,
	})
}

func validateDispatch(dispatch runner.Dispatch) error {
	if dispatch.SchemaVersion != runner.ProtocolVersion || dispatch.RunID == "" || dispatch.ExperimentID == "" || dispatch.SpecRevision == "" || dispatch.HandoffSHA256 == "" || dispatch.RegistrationSHA256 == "" {
		return fmt.Errorf("fixture received incomplete or unsupported runner dispatch")
	}
	if dispatch.Handoff.ExperimentID != dispatch.ExperimentID || dispatch.Handoff.SpecRevision != dispatch.SpecRevision {
		return fmt.Errorf("fixture dispatch handoff binding is inconsistent")
	}
	return nil
}
