package capabilityeval

import "testing"

func TestProtectedScopedCapabilityCorpus(t *testing.T) {
	report, err := EvaluateFile("../../evals/capabilities.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("scoped capability evaluation failed: metrics=%+v failures=%v", report.Metrics, report.Failures)
	}
	if report.Metrics.ScopeLeakage != 0 {
		t.Fatalf("scope leakage = %v, want 0", report.Metrics.ScopeLeakage)
	}
}
