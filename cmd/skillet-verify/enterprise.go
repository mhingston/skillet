package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type enterpriseAssertion struct {
	Name       string `json:"name"`
	SourceStep string `json:"source_step"`
	Passed     bool   `json:"passed"`
	Observed   any    `json:"observed,omitempty"`
	Expected   any    `json:"expected,omitempty"`
}

type enterpriseAcceptanceReport struct {
	SchemaVersion int                   `json:"schema_version"`
	Suite         string                `json:"suite"`
	Passed        bool                  `json:"passed"`
	Assertions    []enterpriseAssertion `json:"assertions"`
	DeferredScope []string              `json:"deferred_scope"`
}

func writeEnterpriseAcceptanceReport(path string, steps []stepResult) (bool, error) {
	stepPassed := make(map[string]bool, len(steps))
	for _, step := range steps {
		stepPassed[step.Name] = step.Passed
	}

	entra := stepPassed["enterprise-entra-oidc-e2e"]
	claimsWorkflow := stepPassed["offline-m2-claims-e2e"]
	malformed := stepPassed["enterprise-malformed-claims"]
	compatibility := stepPassed["enterprise-static-development-regression"]
	ranking := stepPassed["enterprise-ranking-isolation"]
	audit := stepPassed["enterprise-audit-degradation"]

	leakageObserved := any(nil)
	if ranking {
		leakageObserved = 0
	}

	report := enterpriseAcceptanceReport{
		SchemaVersion: 1,
		Suite:         "m2-enterprise-acceptance",
		Passed:        entra && claimsWorkflow && malformed && compatibility && ranking && audit,
		Assertions: []enterpriseAssertion{
			{Name: "delegated_user_authorization", SourceStep: "enterprise-entra-oidc-e2e", Passed: entra, Observed: entra, Expected: true},
			{Name: "workload_identity_authorization", SourceStep: "enterprise-entra-oidc-e2e", Passed: entra, Observed: entra, Expected: true},
			{Name: "allow_deny_scope_matrix", SourceStep: "enterprise-entra-oidc-e2e", Passed: entra, Observed: entra, Expected: true},
			{Name: "capability_knowledge_materialization_claims_flow", SourceStep: "offline-m2-claims-e2e", Passed: claimsWorkflow, Observed: claimsWorkflow, Expected: true},
			{Name: "cross_organization_leakage", SourceStep: "enterprise-ranking-isolation", Passed: ranking, Observed: leakageObserved, Expected: 0},
			{Name: "missing_malformed_untrusted_claims_fail_closed", SourceStep: "enterprise-malformed-claims", Passed: malformed, Observed: malformed, Expected: true},
			{Name: "static_development_auth_regression", SourceStep: "enterprise-static-development-regression", Passed: compatibility, Observed: compatibility, Expected: true},
			{Name: "authorization_metadata_has_no_ranking_effect", SourceStep: "enterprise-ranking-isolation", Passed: ranking, Observed: ranking, Expected: true},
			{Name: "audit_export_failure_preserves_authoritative_state", SourceStep: "enterprise-audit-degradation", Passed: audit, Observed: audit, Expected: true},
		},
		DeferredScope: []string{
			"SCIM provisioning",
			"Microsoft Graph group expansion",
			"provider-specific SIEM integrations",
		},
	}

	contents, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode enterprise acceptance report: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return false, fmt.Errorf("write enterprise acceptance report: %w", err)
	}
	return report.Passed, nil
}
