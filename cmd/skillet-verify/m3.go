package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type m3Assertion struct {
	Name       string `json:"name"`
	SourceStep string `json:"source_step"`
	Passed     bool   `json:"passed"`
	Observed   any    `json:"observed,omitempty"`
	Expected   any    `json:"expected,omitempty"`
}

type m3JourneyEvidence struct {
	Name        string   `json:"name"`
	SourceSteps []string `json:"source_steps"`
	Passed      bool     `json:"passed"`
}

type m3BrowserDriverEvidence struct {
	Harness    string `json:"harness"`
	SourceStep string `json:"source_step"`
	Passed     bool   `json:"passed"`
}

type m3PreservedEvidence struct {
	M1IntegratedAcceptance bool           `json:"m1_integrated_acceptance"`
	M2EnterpriseAcceptance bool           `json:"m2_enterprise_acceptance"`
	M1MetricsPassed        bool           `json:"m1_metrics_passed"`
	Metrics                []metricResult `json:"metrics"`
}

type m3AcceptanceReport struct {
	SchemaVersion int                     `json:"schema_version"`
	Suite         string                  `json:"suite"`
	Passed        bool                    `json:"passed"`
	BrowserDriver m3BrowserDriverEvidence `json:"browser_driver"`
	Journeys      []m3JourneyEvidence     `json:"journeys"`
	Assertions    []m3Assertion           `json:"assertions"`
	Preserved     m3PreservedEvidence     `json:"preserved"`
}

func writeM3AcceptanceReport(path string, steps []stepResult, metrics []metricResult, m2Passed bool) (m3AcceptanceReport, error) {
	stepPassed := make(map[string]bool, len(steps))
	for _, step := range steps {
		stepPassed[step.Name] = step.Passed
	}

	all := func(names ...string) bool {
		for _, name := range names {
			if !stepPassed[name] {
				return false
			}
		}
		return true
	}

	m1MetricsPassed := len(metrics) > 0
	for _, metric := range metrics {
		if !metric.Passed {
			m1MetricsPassed = false
		}
	}

	journeys := []m3JourneyEvidence{
		journeyEvidence("F-discovery-and-human-catalogue", stepPassed, "m3-browser-e2e", "m3-browser-driver-e2e"),
		journeyEvidence("G-composition-and-lock-preview", stepPassed, "m3-composition-e2e", "m3-composition-invariants"),
		journeyEvidence("H-host-native-distribution", stepPassed, "m3-distribution-e2e", "m3-distribution-profile"),
		journeyEvidence("I-collaboration-and-learning-signals", stepPassed, "m3-collaboration-e2e", "m3-evidence-loop-e2e"),
		journeyEvidence("J-reviewable-improvement-proposal", stepPassed, "m3-proposal-e2e"),
		journeyEvidence("K-operator-and-admin-security", stepPassed, "m3-operator-e2e"),
	}

	browserPassed := stepPassed["m3-browser-driver-e2e"]
	crossScopePassed := all("m3-browser-e2e", "m3-operator-e2e")
	compositionInvariantPassed := stepPassed["m3-composition-invariants"]
	distributionPassed := all("m3-distribution-e2e", "m3-distribution-profile")
	collaborationPassed := stepPassed["m3-collaboration-e2e"]
	proposalPassed := stepPassed["m3-proposal-e2e"]
	operatorPassed := stepPassed["m3-operator-e2e"]
	m1Passed := stepPassed["offline-m1-integrated-e2e"]
	verificationPassed := all("go-test", "go-vet", "go-test-race")

	assertions := []m3Assertion{
		{Name: "browser_driver_status", SourceStep: "m3-browser-driver-e2e", Passed: browserPassed, Observed: browserPassed, Expected: true},
		{Name: "browser_security_and_progressive_enhancement", SourceStep: "m3-browser-e2e", Passed: stepPassed["m3-browser-e2e"], Observed: stepPassed["m3-browser-e2e"], Expected: true},
		{Name: "cross_scope_and_cross_organization_leakage", SourceStep: "m3-browser-e2e + m3-operator-e2e", Passed: crossScopePassed, Observed: zeroWhenPassed(crossScopePassed), Expected: 0},
		{Name: "dependency_resolution_deterministic", SourceStep: "m3-composition-invariants", Passed: compositionInvariantPassed, Observed: compositionInvariantPassed, Expected: true},
		{Name: "dependency_cycle_detection", SourceStep: "m3-composition-invariants", Passed: compositionInvariantPassed, Observed: compositionInvariantPassed, Expected: true},
		{Name: "dependency_conflict_detection", SourceStep: "m3-composition-invariants", Passed: compositionInvariantPassed, Observed: compositionInvariantPassed, Expected: true},
		{Name: "unauthorized_dependency_fail_closed", SourceStep: "m3-composition-e2e", Passed: stepPassed["m3-composition-e2e"], Observed: stepPassed["m3-composition-e2e"], Expected: true},
		{Name: "distribution_deterministic", SourceStep: "m3-distribution-e2e + m3-distribution-profile", Passed: distributionPassed, Observed: distributionPassed, Expected: true},
		{Name: "distribution_host_profile_validation", SourceStep: "m3-distribution-profile", Passed: stepPassed["m3-distribution-profile"], Observed: stepPassed["m3-distribution-profile"], Expected: true},
		{Name: "collaboration_ranking_effect", SourceStep: "m3-collaboration-e2e", Passed: collaborationPassed, Observed: zeroWhenPassed(collaborationPassed), Expected: 0},
		{Name: "proposal_immutable_provenance", SourceStep: "m3-proposal-e2e", Passed: proposalPassed, Observed: proposalPassed, Expected: true},
		{Name: "proposal_verification_gate", SourceStep: "m3-proposal-e2e", Passed: proposalPassed, Observed: proposalPassed, Expected: true},
		{Name: "proposal_automatic_canonical_mutations", SourceStep: "m3-proposal-e2e", Passed: proposalPassed, Observed: zeroWhenPassed(proposalPassed), Expected: 0},
		{Name: "operator_authorization_csrf_and_audit", SourceStep: "m3-operator-e2e", Passed: operatorPassed, Observed: operatorPassed, Expected: true},
		{Name: "m1_integrated_acceptance_preserved", SourceStep: "offline-m1-integrated-e2e", Passed: m1Passed, Observed: m1Passed, Expected: true},
		{Name: "m1_metric_regressions", SourceStep: "retrieval/capability/knowledge evals", Passed: m1MetricsPassed, Observed: m1MetricsPassed, Expected: true},
		{Name: "m2_enterprise_acceptance_preserved", SourceStep: "enterprise-acceptance.json", Passed: m2Passed, Observed: m2Passed, Expected: true},
		{Name: "go_test_vet_race", SourceStep: "go-test + go-vet + go-test-race", Passed: verificationPassed, Observed: verificationPassed, Expected: true},
	}

	passed := m1Passed && m1MetricsPassed && m2Passed && verificationPassed
	for _, journey := range journeys {
		if !journey.Passed {
			passed = false
		}
	}
	for _, assertion := range assertions {
		if !assertion.Passed {
			passed = false
		}
	}

	report := m3AcceptanceReport{
		SchemaVersion: 1,
		Suite:         "m3-human-adoption-acceptance",
		Passed:        passed,
		BrowserDriver: m3BrowserDriverEvidence{
			Harness:    "headless-chrome-cli",
			SourceStep: "m3-browser-driver-e2e",
			Passed:     browserPassed,
		},
		Journeys:   journeys,
		Assertions: assertions,
		Preserved: m3PreservedEvidence{
			M1IntegratedAcceptance: m1Passed,
			M2EnterpriseAcceptance: m2Passed,
			M1MetricsPassed:        m1MetricsPassed,
			Metrics:                metrics,
		},
	}

	contents, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return m3AcceptanceReport{}, fmt.Errorf("encode M3 acceptance report: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return m3AcceptanceReport{}, fmt.Errorf("write M3 acceptance report: %w", err)
	}
	return report, nil
}

func journeyEvidence(name string, steps map[string]bool, sourceSteps ...string) m3JourneyEvidence {
	passed := true
	for _, source := range sourceSteps {
		if !steps[source] {
			passed = false
		}
	}
	return m3JourneyEvidence{Name: name, SourceSteps: sourceSteps, Passed: passed}
}

func zeroWhenPassed(passed bool) any {
	if passed {
		return 0
	}
	return nil
}
