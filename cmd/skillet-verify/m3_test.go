package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteM3AcceptanceReport(t *testing.T) {
	steps := []stepResult{
		{Name: "go-test", Passed: true},
		{Name: "go-vet", Passed: true},
		{Name: "go-test-race", Passed: true},
		{Name: "offline-m1-integrated-e2e", Passed: true},
		{Name: "m3-browser-e2e", Passed: true},
		{Name: "m3-browser-driver-e2e", Passed: true},
		{Name: "m3-composition-e2e", Passed: true},
		{Name: "m3-composition-invariants", Passed: true},
		{Name: "m3-distribution-e2e", Passed: true},
		{Name: "m3-distribution-profile", Passed: true},
		{Name: "m3-collaboration-e2e", Passed: true},
		{Name: "m3-evidence-loop-e2e", Passed: true},
		{Name: "m3-proposal-e2e", Passed: true},
		{Name: "m3-operator-e2e", Passed: true},
	}
	metrics := []metricResult{{Name: "single_top1_accuracy", Passed: true}}
	path := filepath.Join(t.TempDir(), "m3-acceptance.json")
	report, err := writeM3AcceptanceReport(path, steps, metrics, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("M3 acceptance unexpectedly failed: %+v", report)
	}
	if report.BrowserDriver.Harness != "headless-chrome-cli" || !report.BrowserDriver.Passed {
		t.Fatalf("browser evidence = %+v", report.BrowserDriver)
	}
	if len(report.Journeys) != 6 {
		t.Fatalf("journey count=%d, want 6", len(report.Journeys))
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded m3AcceptanceReport
	if err := json.Unmarshal(contents, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Preserved.M1IntegratedAcceptance || !decoded.Preserved.M2EnterpriseAcceptance || !decoded.Preserved.M1MetricsPassed {
		t.Fatalf("preserved evidence = %+v", decoded.Preserved)
	}
	assertM3NumericAssertion(t, decoded, "cross_scope_and_cross_organization_leakage", 0)
	assertM3NumericAssertion(t, decoded, "collaboration_ranking_effect", 0)
	assertM3NumericAssertion(t, decoded, "proposal_automatic_canonical_mutations", 0)
}

func TestWriteM3AcceptanceReportFailsClosedWhenProofMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m3-acceptance.json")
	report, err := writeM3AcceptanceReport(path, []stepResult{
		{Name: "go-test", Passed: true},
		{Name: "go-vet", Passed: true},
		{Name: "go-test-race", Passed: true},
		{Name: "offline-m1-integrated-e2e", Passed: true},
	}, []metricResult{{Name: "single_top1_accuracy", Passed: true}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("incomplete M3 proof unexpectedly passed")
	}
}

func TestWriteM3AcceptanceReportFailsClosedOnM1OrM2Regression(t *testing.T) {
	steps := []stepResult{
		{Name: "go-test", Passed: true},
		{Name: "go-vet", Passed: true},
		{Name: "go-test-race", Passed: true},
		{Name: "offline-m1-integrated-e2e", Passed: true},
		{Name: "m3-browser-e2e", Passed: true},
		{Name: "m3-browser-driver-e2e", Passed: true},
		{Name: "m3-composition-e2e", Passed: true},
		{Name: "m3-composition-invariants", Passed: true},
		{Name: "m3-distribution-e2e", Passed: true},
		{Name: "m3-distribution-profile", Passed: true},
		{Name: "m3-collaboration-e2e", Passed: true},
		{Name: "m3-evidence-loop-e2e", Passed: true},
		{Name: "m3-proposal-e2e", Passed: true},
		{Name: "m3-operator-e2e", Passed: true},
	}
	path := filepath.Join(t.TempDir(), "m3-acceptance.json")
	report, err := writeM3AcceptanceReport(path, steps, []metricResult{{Name: "single_top1_accuracy", Passed: false}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("M1 metric regression unexpectedly passed M3")
	}
	report, err = writeM3AcceptanceReport(path, steps, []metricResult{{Name: "single_top1_accuracy", Passed: true}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed {
		t.Fatal("M2 regression unexpectedly passed M3")
	}
}

func assertM3NumericAssertion(t *testing.T, report m3AcceptanceReport, name string, expected float64) {
	t.Helper()
	for _, assertion := range report.Assertions {
		if assertion.Name != name {
			continue
		}
		if !assertion.Passed || assertion.Observed != expected || assertion.Expected != expected {
			t.Fatalf("%s = %+v", name, assertion)
		}
		return
	}
	t.Fatalf("assertion %q missing", name)
}
