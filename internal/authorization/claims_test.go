package authorization

import (
	"context"
	"testing"

	authn "github.com/mhingston/skillet/internal/auth"
)

func TestClaimsPolicyAllowDenyMatrix(t *testing.T) {
	policy, err := NewClaimsPolicy([]Grant{
		{
			Permissions: []string{"capability.reader"},
			Attributes:  map[string][]string{"groups": {"platform"}},
			Actions:     []Action{ActionCapabilitySearch, ActionCapabilityDescribe, ActionCapabilityMaterialize},
			Resources: []ResourceRule{
				{Namespace: "engineering", Repository: "api"},
				{Namespace: "engineering", Repository: "worker", IDs: []string{"capability-allowed"}},
			},
		},
		{
			Permissions: []string{"knowledge.reader"},
			Actions:     []Action{ActionKnowledgeSearch, ActionKnowledgeRead},
			Resources:   []ResourceRule{{}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	base := authn.Identity{
		Subject:        "user-1",
		OrganizationID: "acme",
		Permissions: map[string]struct{}{
			"capability.reader": {},
			"knowledge.reader":  {},
		},
		Attributes: map[string][]string{"groups": {"platform"}},
	}

	tests := []struct {
		name     string
		identity authn.Identity
		action   Action
		resource Resource
		allowed  bool
		reason   string
	}{
		{name: "repo allow", identity: base, action: ActionCapabilitySearch, resource: Resource{OrganizationID: "acme", Namespace: "engineering", Repository: "api"}, allowed: true, reason: ReasonAllowed},
		{name: "stable id allow", identity: base, action: ActionCapabilityDescribe, resource: Resource{OrganizationID: "acme", Namespace: "engineering", Repository: "worker", ID: "capability-allowed"}, allowed: true, reason: ReasonAllowed},
		{name: "stable id deny", identity: base, action: ActionCapabilityDescribe, resource: Resource{OrganizationID: "acme", Namespace: "engineering", Repository: "worker", ID: "capability-other"}, reason: ReasonNotEntitled},
		{name: "wrong repo deny", identity: base, action: ActionCapabilitySearch, resource: Resource{OrganizationID: "acme", Namespace: "engineering", Repository: "payments"}, reason: ReasonNotEntitled},
		{name: "wrong namespace deny", identity: base, action: ActionCapabilitySearch, resource: Resource{OrganizationID: "acme", Namespace: "sales", Repository: "api"}, reason: ReasonNotEntitled},
		{name: "knowledge org allow", identity: base, action: ActionKnowledgeRead, resource: Resource{OrganizationID: "acme", ID: "chunk-1"}, allowed: true, reason: ReasonAllowed},
		{name: "wrong org deny", identity: base, action: ActionKnowledgeRead, resource: Resource{OrganizationID: "other", ID: "chunk-1"}, reason: ReasonOrganizationMismatch},
		{name: "unmapped action deny", identity: base, action: ActionEvidenceReview, resource: Resource{OrganizationID: "acme", ID: "evidence-1"}, reason: ReasonNotEntitled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := policy.Authorize(context.Background(), tt.identity, tt.action, tt.resource)
			if decision.Allowed != tt.allowed || decision.Reason != tt.reason {
				t.Fatalf("decision = %+v, want allowed=%v reason=%q", decision, tt.allowed, tt.reason)
			}
		})
	}
}

func TestClaimsPolicyMissingOrUnmappedSelectorsFailClosed(t *testing.T) {
	policy, err := NewClaimsPolicy([]Grant{{
		Permissions: []string{"reader"},
		Attributes:  map[string][]string{"groups": {"engineering"}},
		Actions:     []Action{ActionCapabilitySearch},
		Resources:   []ResourceRule{{Namespace: "engineering", Repository: "api"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resource := Resource{OrganizationID: "acme", Namespace: "engineering", Repository: "api"}

	for _, identity := range []authn.Identity{
		{Subject: "missing-permission", OrganizationID: "acme", Attributes: map[string][]string{"groups": {"engineering"}}},
		{Subject: "missing-attribute", OrganizationID: "acme", Permissions: map[string]struct{}{"reader": {}}},
		{Subject: "unmapped-role", OrganizationID: "acme", Permissions: map[string]struct{}{"admin": {}}, Attributes: map[string][]string{"groups": {"engineering"}}},
		{Subject: "unmapped-group", OrganizationID: "acme", Permissions: map[string]struct{}{"reader": {}}, Attributes: map[string][]string{"groups": {"sales"}}},
	} {
		decision := policy.Authorize(context.Background(), identity, ActionCapabilitySearch, resource)
		if decision.Allowed || decision.Reason != ReasonNotEntitled {
			t.Fatalf("identity %q decision = %+v", identity.Subject, decision)
		}
	}
}

func TestClaimsPolicyValidation(t *testing.T) {
	tests := []struct {
		name   string
		grants []Grant
	}{
		{name: "empty"},
		{name: "no selector", grants: []Grant{{Actions: []Action{ActionKnowledgeRead}, Resources: []ResourceRule{{}}}}},
		{name: "no action", grants: []Grant{{Permissions: []string{"reader"}, Resources: []ResourceRule{{}}}}},
		{name: "unknown action", grants: []Grant{{Permissions: []string{"reader"}, Actions: []Action{"unknown"}, Resources: []ResourceRule{{}}}}},
		{name: "no resource", grants: []Grant{{Permissions: []string{"reader"}, Actions: []Action{ActionKnowledgeRead}}}},
		{name: "repository without namespace", grants: []Grant{{Permissions: []string{"reader"}, Actions: []Action{ActionKnowledgeRead}, Resources: []ResourceRule{{Repository: "api"}}}}},
		{name: "blank permission", grants: []Grant{{Permissions: []string{" "}, Actions: []Action{ActionKnowledgeRead}, Resources: []ResourceRule{{}}}}},
		{name: "blank attribute value", grants: []Grant{{Attributes: map[string][]string{"groups": {""}}, Actions: []Action{ActionKnowledgeRead}, Resources: []ResourceRule{{}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewClaimsPolicy(tt.grants); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}
