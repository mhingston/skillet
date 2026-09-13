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
	for _, kind := range []string{"skill", "playbook", "tool"} {
		if report.Metrics.RecallByKind[kind] != 1 {
			t.Fatalf("%s recall = %v, want 1", kind, report.Metrics.RecallByKind[kind])
		}
		if report.Metrics.KindConfusion[kind][kind] == 0 {
			t.Fatalf("kind confusion lacks correct %s routing: %+v", kind, report.Metrics.KindConfusion)
		}
	}
	if report.Metrics.KindConfusion["none"]["none"] == 0 {
		t.Fatalf("negative kind confusion missing: %+v", report.Metrics.KindConfusion)
	}
}
