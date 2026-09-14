package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteEnterpriseAcceptanceReport(t *testing.T) {
	steps := []stepResult{
		{Name: "enterprise-entra-oidc-e2e", Passed: true},
		{Name: "offline-m2-claims-e2e", Passed: true},
		{Name: "enterprise-malformed-claims", Passed: true},
		{Name: "enterprise-static-development-regression", Passed: true},
		{Name: "enterprise-ranking-isolation", Passed: true},
		{Name: "enterprise-audit-degradation", Passed: true},
	}
	path := filepath.Join(t.TempDir(), "enterprise-acceptance.json")
	passed, err := writeEnterpriseAcceptanceReport(path, steps)
	if err != nil {
		t.Fatal(err)
	}
	if !passed {
		t.Fatal("enterprise acceptance unexpectedly failed")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report enterpriseAcceptanceReport
	if err := json.Unmarshal(contents, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.Suite != "m2-enterprise-acceptance" {
		t.Fatalf("report = %+v", report)
	}
	var foundLeakage bool
	for _, assertion := range report.Assertions {
		if assertion.Name != "cross_organization_leakage" {
			continue
		}
		foundLeakage = true
		if !assertion.Passed || assertion.Observed != float64(0) || assertion.Expected != float64(0) {
			t.Fatalf("cross-organization evidence = %+v", assertion)
		}
	}
	if !foundLeakage {
		t.Fatal("cross-organization leakage assertion missing")
	}
}

func TestWriteEnterpriseAcceptanceReportFailsClosedWhenProofMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enterprise-acceptance.json")
	passed, err := writeEnterpriseAcceptanceReport(path, []stepResult{{Name: "enterprise-entra-oidc-e2e", Passed: true}})
	if err != nil {
		t.Fatal(err)
	}
	if passed {
		t.Fatal("incomplete enterprise proof unexpectedly passed")
	}
}
