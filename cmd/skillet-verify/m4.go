package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type m4FixtureEvidence struct {
	SchemaVersion int               `json:"schema_version"`
	Suite         string            `json:"suite"`
	Passed        bool              `json:"passed"`
	Journeys      map[string]bool   `json:"journeys"`
	Assertions    map[string]any    `json:"assertions"`
	Digests       map[string]string `json:"digests"`
	Decisions     map[string]string `json:"decisions"`
	Counts        map[string]int    `json:"counts"`
	Preserved     map[string]bool   `json:"preserved"`
}

type m4Assertion struct {
	Name       string `json:"name"`
	SourceStep string `json:"source_step"`
	Passed     bool   `json:"passed"`
	Observed   any    `json:"observed,omitempty"`
	Expected   any    `json:"expected,omitempty"`
}

type m4JourneyEvidence struct {
	Name        string   `json:"name"`
	SourceSteps []string `json:"source_steps"`
	Passed      bool     `json:"passed"`
}

type m4PreservedEvidence struct {
	M1IntegratedAcceptance bool `json:"m1_integrated_acceptance"`
	M2EnterpriseAcceptance bool `json:"m2_enterprise_acceptance"`
	M3Acceptance           bool `json:"m3_acceptance"`
	M1MetricsPassed        bool `json:"m1_metrics_passed"`
}

type m4AcceptanceReport struct {
	SchemaVersion int                 `json:"schema_version"`
	Suite         string              `json:"suite"`
	Passed        bool                `json:"passed"`
	Journeys      []m4JourneyEvidence `json:"journeys"`
	Assertions    []m4Assertion       `json:"assertions"`
	Digests       map[string]string   `json:"digests"`
	Decisions     map[string]string   `json:"decisions"`
	Counts        map[string]int      `json:"counts"`
	Preserved     m4PreservedEvidence `json:"preserved"`
}

var m4JourneyNames = []string{
	"L-default-off-isolation",
	"M-reproducible-experiment",
	"N-lineage-and-comparison",
	"O-improver-meta-evidence",
	"P-curriculum-evaluator-protection",
	"Q-runner-security-boundary",
}

var m4RequiredAssertions = []string{
	"m4_default_off",
	"explicit_enablement_required",
	"authorized_mcp_path_used",
	"handoff_deterministic",
	"provenance_unknowns_explicit",
	"lineage_deterministic",
	"lineage_cycle_rejected",
	"protected_regression_enforced",
	"meta_eval_compatible_held_out_only",
	"protected_evaluator_immutable",
	"held_out_not_in_candidate_handoff",
	"runner_tamper_rejected",
	"runner_replay_idempotent",
	"runner_mismatch_rejected",
	"stale_result_rejected",
	"cross_scope_rejected",
	"no_source_mutation",
	"no_ranking_change",
	"no_governance_change",
	"no_external_runtime_dependency",
}

var m4RequiredDigests = []string{
	"experiment_handoff_sha256",
	"experiment_spec_revision",
	"runner_dispatch_sha256",
	"runner_result_sha256",
	"strategy_config_sha256",
	"meta_eval_definition_revision",
}

func writeM4AcceptanceReport(path, fixturePath string, steps []stepResult, m3 m3AcceptanceReport) (m4AcceptanceReport, error) {
	fixtureContents, err := readM4FixtureEvidence(fixturePath)
	if err != nil {
		return m4AcceptanceReport{}, fmt.Errorf("read M4 fixture evidence: %w", err)
	}
	var fixture m4FixtureEvidence
	if err := json.Unmarshal(fixtureContents, &fixture); err != nil {
		return m4AcceptanceReport{}, fmt.Errorf("decode M4 fixture evidence: %w", err)
	}
	if fixture.SchemaVersion != 1 || fixture.Suite != "m4-opt-in-capability-evolution" {
		return m4AcceptanceReport{}, fmt.Errorf("unsupported M4 fixture evidence schema/suite: schema=%d suite=%q", fixture.SchemaVersion, fixture.Suite)
	}

	stepPassed := false
	for _, step := range steps {
		if step.Name == "m4-integrated-e2e" {
			stepPassed = step.Passed
			break
		}
	}

	report := m4AcceptanceReport{
		SchemaVersion: 1,
		Suite:         "m4-opt-in-capability-evolution-acceptance",
		Passed:        fixture.Passed && stepPassed,
		Digests:       fixture.Digests,
		Decisions:     fixture.Decisions,
		Counts:        fixture.Counts,
		Preserved: m4PreservedEvidence{
			M1IntegratedAcceptance: m3.Preserved.M1IntegratedAcceptance,
			M2EnterpriseAcceptance: m3.Preserved.M2EnterpriseAcceptance,
			M3Acceptance:           m3.Passed,
			M1MetricsPassed:        m3.Preserved.M1MetricsPassed,
		},
	}

	for _, name := range m4JourneyNames {
		passed := stepPassed && fixture.Journeys[name]
		report.Journeys = append(report.Journeys, m4JourneyEvidence{Name: name, SourceSteps: []string{"m4-integrated-e2e"}, Passed: passed})
		if !passed {
			report.Passed = false
		}
	}
	for _, name := range m4RequiredAssertions {
		observed, exists := fixture.Assertions[name]
		passed := exists && observed == true
		report.Assertions = append(report.Assertions, m4Assertion{Name: name, SourceStep: "m4-integrated-e2e", Passed: passed, Observed: observed, Expected: true})
		if !passed {
			report.Passed = false
		}
	}
	for _, name := range m4RequiredDigests {
		observed := strings.TrimSpace(fixture.Digests[name])
		passed := observed != ""
		report.Assertions = append(report.Assertions, m4Assertion{Name: name + "_present", SourceStep: "m4-integrated-e2e", Passed: passed, Observed: observed, Expected: "non-empty immutable digest/revision"})
		if !passed {
			report.Passed = false
		}
	}

	preserved := []struct {
		name   string
		passed bool
	}{
		{"m1_integrated_acceptance_preserved", report.Preserved.M1IntegratedAcceptance},
		{"m2_enterprise_acceptance_preserved", report.Preserved.M2EnterpriseAcceptance},
		{"m3_acceptance_preserved", report.Preserved.M3Acceptance},
		{"m1_protected_metrics_preserved", report.Preserved.M1MetricsPassed},
	}
	for _, item := range preserved {
		report.Assertions = append(report.Assertions, m4Assertion{Name: item.name, SourceStep: "parent M1-M3 verification evidence", Passed: item.passed, Observed: item.passed, Expected: true})
		if !item.passed {
			report.Passed = false
		}
	}

	contents, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return m4AcceptanceReport{}, fmt.Errorf("encode M4 acceptance report: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return m4AcceptanceReport{}, fmt.Errorf("write M4 acceptance report: %w", err)
	}
	return report, nil
}

func readM4FixtureEvidence(path string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if err == nil || !os.IsNotExist(err) || filepath.IsAbs(path) {
		return contents, err
	}

	// Go test executes package tests with the package directory as cwd. The
	// verifier intentionally passes its report path through the environment so
	// the focused E2E remains runnable on its own; normalize that package-relative
	// output back into the verifier's repository-root artifact directory.
	packageRelative := filepath.Join("internal", "e2e", path)
	contents, nestedErr := os.ReadFile(packageRelative)
	if nestedErr != nil {
		return nil, err
	}
	if writeErr := os.WriteFile(path, contents, 0o644); writeErr != nil {
		return nil, fmt.Errorf("normalize package-relative M4 fixture evidence: %w", writeErr)
	}
	return contents, nil
}
