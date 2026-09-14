package httpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	authn "github.com/mhingston/skillet/internal/auth"
	authz "github.com/mhingston/skillet/internal/authorization"
)

func TestAuthorizationDenialAuditContainsOnlyBoundedPolicyMetadata(t *testing.T) {
	s, _, _ := lockedMaterializeFixture(t)
	policy, err := authz.NewClaimsPolicy([]authz.Grant{{
		Permissions: []string{"capability.reader"},
		Attributes:  map[string][]string{"groups": {"platform"}},
		Actions:     []authz.Action{authz.ActionCapabilityDescribe},
		Resources: []authz.ResourceRule{{
			Namespace:  "engineering",
			Repository: "repo-a",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureAuthorization(policy)
	t.Cleanup(func() { s.ConfigureAuthorization(nil) })

	ctx := withAuthenticatedIdentity(context.Background(), authn.Identity{
		Subject:        "sensitive-subject",
		OrganizationID: "demo",
		Permissions:    map[string]struct{}{"capability.reader": {}},
		Attributes:     map[string][]string{"groups": {"platform", "sensitive-group"}},
	})
	resource := authz.Resource{
		OrganizationID: "demo",
		Namespace:      "engineering",
		Repository:     "repo-b",
		ID:             "demo/repo-b/release",
	}
	if err := s.authorize(ctx, authz.ActionCapabilityDescribe, resource); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("authorization error = %v, want denial", err)
	}

	var details string
	if err := s.catalogue.DB.QueryRowContext(ctx, `SELECT details_json FROM audit_events WHERE organization_id=? AND event_type='authorization_denied' ORDER BY id DESC LIMIT 1`, "demo").Scan(&details); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"action":"capability.describe"`, `"reason":"not_entitled"`, `"namespace":"engineering"`, `"repository":"repo-b"`, `"resource_id":"demo/repo-b/release"`} {
		if !strings.Contains(details, want) {
			t.Fatalf("authorization denial audit missing %q: %s", want, details)
		}
	}
	for _, forbidden := range []string{"capability.reader", "platform", "sensitive-group", "sensitive-subject", "Bearer"} {
		if strings.Contains(details, forbidden) {
			t.Fatalf("authorization denial audit leaked %q: %s", forbidden, details)
		}
	}
}
