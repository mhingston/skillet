package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteM4AcceptanceReportPreservesParentGateAndFixtureEvidence(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "m4-integrated.json")
	reportPath := filepath.Join(dir, "m4-acceptance.json")
	journeys := map[string]bool{}
	for _, name := range m4JourneyNames {
		journeys[name] = true
	}
	assertions := map[string]any{}
	for _, name := range m4RequiredAssertions {
		assertions[name] = true
	}
	digests := map[string]string{}
	for _, name := range m4RequiredDigests {
		digests[name] = "sha256:fixture-" + name
	}
	fixture := m4FixtureEvidence{
		SchemaVersion: 1,
		Suite:         "m4-opt-in-capability-evolution",
		Passed:        true,
		Journeys:      journeys,
		Assertions:    assertions,
		Digests:       digests,
		Decisions:     map[string]string{"challenger_a_held_out": "fails_gate", "challenger_b_held_out": "passes_gate"},
		Counts:        map[string]int{"competing_descendants": 2},
	}
	contents, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixturePath, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	m3 := m3AcceptanceReport{
		Passed: true,
		Preserved: m3PreservedEvidence{
			M1IntegratedAcceptance: true,
			M2EnterpriseAcceptance: true,
			M1MetricsPassed:        true,
		},
	}
	report, err := writeM4AcceptanceReport(reportPath, fixturePath, []stepResult{{Name: "m4-integrated-e2e", Passed: true}}, m3)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || len(report.Journeys) != len(m4JourneyNames) || len(report.Assertions) != len(m4RequiredAssertions)+len(m4RequiredDigests)+4 {
		t.Fatalf("unexpected M4 acceptance report: %+v", report)
	}
	if !report.Preserved.M1IntegratedAcceptance || !report.Preserved.M2EnterpriseAcceptance || !report.Preserved.M3Acceptance || !report.Preserved.M1MetricsPassed {
		t.Fatalf("parent gate evidence was not preserved: %+v", report.Preserved)
	}
	if report.Digests["runner_result_sha256"] == "" || report.Counts["competing_descendants"] != 2 {
		t.Fatalf("fixture evidence missing from report: %+v", report)
	}
}

func TestWriteM4AcceptanceReportFailsClosedOnMissingAssertion(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "m4-integrated.json")
	journeys := map[string]bool{}
	for _, name := range m4JourneyNames {
		journeys[name] = true
	}
	assertions := map[string]any{}
	for _, name := range m4RequiredAssertions {
		assertions[name] = true
	}
	delete(assertions, "runner_tamper_rejected")
	digests := map[string]string{}
	for _, name := range m4RequiredDigests {
		digests[name] = "fixture"
	}
	contents, err := json.Marshal(m4FixtureEvidence{SchemaVersion: 1, Suite: "m4-opt-in-capability-evolution", Passed: true, Journeys: journeys, Assertions: assertions, Digests: digests})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixturePath, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	m3 := m3AcceptanceReport{Passed: true, Preserved: m3PreservedEvidence{M1IntegratedAcceptance: true, M2EnterpriseAcceptance: true, M1MetricsPassed: true}}
	report, err := writeM4AcceptanceReport(filepath.Join(dir, "report.json"), fixturePath, []stepResult{{Name: "m4-integrated-e2e", Passed: true}}, m3)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("M4 report passed despite missing required security assertion")
	}
}
