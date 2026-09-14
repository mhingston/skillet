package authorization

import (
	"context"
	"testing"

	authn "github.com/mhingston/skillet/internal/auth"
)

func TestCompatibilityPolicyAllowsSupportedActionsInsideOrganization(t *testing.T) {
	policy := CompatibilityPolicy{}
	identity := authn.Identity{Subject: "user-1", OrganizationID: "acme"}
	resource := Resource{OrganizationID: "acme", Namespace: "engineering", Repository: "api", ID: "capability-1"}

	for _, action := range []Action{
		ActionCapabilitySearch,
		ActionCapabilityDescribe,
		ActionCapabilityMaterialize,
		ActionKnowledgeSearch,
		ActionKnowledgeRead,
		ActionEvidenceReport,
		ActionEvidenceReview,
		ActionGovernanceRead,
		ActionOperatorRead,
		ActionOperatorAuditProbe,
	} {
		t.Run(string(action), func(t *testing.T) {
			decision := policy.Authorize(context.Background(), identity, action, resource)
			if !decision.Allowed || decision.Reason != ReasonAllowed {
				t.Fatalf("decision = %+v", decision)
			}
		})
	}
}

func TestCompatibilityPolicyFailsClosed(t *testing.T) {
	policy := CompatibilityPolicy{}
	validIdentity := authn.Identity{Subject: "user-1", OrganizationID: "acme"}
	validResource := Resource{OrganizationID: "acme"}

	tests := []struct {
		name     string
		identity authn.Identity
		action   Action
		resource Resource
		reason   string
	}{
		{name: "unknown action", identity: validIdentity, action: Action("capability.delete"), resource: validResource, reason: ReasonUnsupportedAction},
		{name: "missing subject", identity: authn.Identity{OrganizationID: "acme"}, action: ActionCapabilitySearch, resource: validResource, reason: ReasonInvalidIdentity},
		{name: "missing identity organization", identity: authn.Identity{Subject: "user-1"}, action: ActionCapabilitySearch, resource: validResource, reason: ReasonInvalidIdentity},
		{name: "missing resource organization", identity: validIdentity, action: ActionCapabilitySearch, resource: Resource{}, reason: ReasonInvalidResource},
		{name: "repository without namespace", identity: validIdentity, action: ActionCapabilitySearch, resource: Resource{OrganizationID: "acme", Repository: "api"}, reason: ReasonInvalidResource},
		{name: "organization mismatch", identity: validIdentity, action: ActionCapabilitySearch, resource: Resource{OrganizationID: "other"}, reason: ReasonOrganizationMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := policy.Authorize(context.Background(), tt.identity, tt.action, tt.resource)
			if decision.Allowed || decision.Reason != tt.reason {
				t.Fatalf("decision = %+v, want denied reason %q", decision, tt.reason)
			}
		})
	}
}

func TestCompatibilityPolicyDoesNotDependOnPermissionsOrAttributes(t *testing.T) {
	policy := CompatibilityPolicy{}
	identity := authn.Identity{
		Subject:        "workload-1",
		OrganizationID: "acme",
		Permissions:    map[string]struct{}{"provider-neutral.permission": {}},
		Attributes:     map[string][]string{"groups": {"engineering"}},
	}
	decision := policy.Authorize(context.Background(), identity, ActionKnowledgeRead, Resource{OrganizationID: "acme", ID: "doc-1"})
	if !decision.Allowed {
		t.Fatalf("decision = %+v", decision)
	}
}
