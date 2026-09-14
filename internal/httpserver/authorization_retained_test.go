package httpserver

import (
	"context"
	"testing"

	authn "github.com/mhingston/skillet/internal/auth"
	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/search"
)

func TestRetainedEvidenceAuthorizationUsesCatalogueRepositoryScope(t *testing.T) {
	s, first, _ := lockedMaterializeFixture(t)
	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := capability.NewScope("demo", "engineering", "skills")
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := capability.New(index, []capability.SourcePolicy{{RepositoryID: "skills", Scope: scope}})
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureCapabilities(capabilities)
	t.Cleanup(func() {
		s.ConfigureAuthorization(nil)
		s.ConfigureCapabilities(nil)
	})
	policy, err := authz.NewClaimsPolicy([]authz.Grant{{
		Permissions: []string{"evidence.reviewer"},
		Actions:     []authz.Action{authz.ActionEvidenceReview},
		Resources: []authz.ResourceRule{{
			Namespace:  "engineering",
			Repository: "skills",
			IDs:        []string{first.SkillID},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureAuthorization(policy)
	ctx := withAuthenticatedIdentity(context.Background(), authn.Identity{
		Subject:        "reviewer",
		OrganizationID: "demo",
		Permissions:    map[string]struct{}{"evidence.reviewer": {}},
	})
	// The capability index is intentionally empty: scope must come from the
	// retained catalogue skill -> repository provenance, not active routing.
	if err := s.authorizeEvidenceResource(ctx, authz.ActionEvidenceReview, "demo", "", first.SkillID); err != nil {
		t.Fatalf("retained evidence authorization failed: %v", err)
	}
}
