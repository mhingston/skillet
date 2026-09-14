package httpserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authn "github.com/mhingston/skillet/internal/auth"
	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/composition"
	"github.com/mhingston/skillet/internal/search"
)

func TestResolveCapabilityPlanDeterministicClosure(t *testing.T) {
	s, token := compositionServerFixture(t)
	plan, err := s.resolveCapabilityPlan(context.Background(), resolveCapabilityPlanInput{CandidateID: token})
	if err != nil { t.Fatal(err) }
	if len(plan.Capabilities) != 2 { t.Fatalf("capabilities=%+v", plan.Capabilities) }
	if plan.Capabilities[0].ID != "demo/repo/base" || plan.Capabilities[1].ID != "demo/repo/root" {
		t.Fatalf("capability order=%+v", plan.Capabilities)
	}
	if len(plan.Edges) != 2 || plan.Edges[1].Relation != "requires" { t.Fatalf("edges=%+v", plan.Edges) }
}

func TestResolveCapabilityPlanAuthorizationBeforeDependencyDisclosure(t *testing.T) {
	s, token := compositionServerFixture(t)
	policy, err := authz.NewClaimsPolicy([]authz.Grant{{
		Permissions: []string{"capability.reader"},
		Actions: []authz.Action{authz.ActionCapabilityDescribe},
		Resources: []authz.ResourceRule{{IDs: []string{"demo/repo/root"}}},
	}})
	if err != nil { t.Fatal(err) }
	s.ConfigureAuthorization(policy)
	t.Cleanup(func() { s.ConfigureAuthorization(nil) })
	ctx := withAuthenticatedIdentity(context.Background(), authn.Identity{
		Subject: "reader", OrganizationID: "demo", Permissions: map[string]struct{}{"capability.reader": {}},
	})
	_, err = s.resolveCapabilityPlan(ctx, resolveCapabilityPlanInput{CandidateID: token})
	if err == nil { t.Fatal("expected unavailable dependency") }
	if strings.Contains(err.Error(), "demo/repo/base") || strings.Contains(err.Error(), "base-rev") {
		t.Fatalf("dependency identity leaked through error: %v", err)
	}
}

func TestResolveCuratedCollectionAndBrowserPreview(t *testing.T) {
	s, _ := compositionServerFixture(t)
	if err := s.ConfigureCollections([]composition.Collection{{
		ID: "review-kit", Name: "Review kit", Description: "Curated review capabilities",
		Members: []composition.Reference{{ID: "demo/repo/root"}},
	}}); err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = s.ConfigureCollections(nil) })

	plan, err := s.resolveCapabilityPlan(context.Background(), resolveCapabilityPlanInput{Collection: "review-kit"})
	if err != nil { t.Fatal(err) }
	if len(plan.Capabilities) != 2 { t.Fatalf("plan=%+v", plan) }

	server := httptest.NewServer(s.Handler("/mcp", 1<<20, AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer server.Close()
	response, err := http.Get(server.URL + "/ui/composition/preview?collection=review-kit")
	if err != nil { t.Fatal(err) }
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil { t.Fatal(err) }
	if response.StatusCode != http.StatusOK { t.Fatalf("status=%d body=%s", response.StatusCode, body) }
	for _, want := range []string{"Locked resolution plan", "demo/repo/base", "demo/repo/root", "Declared reasons and edges", "Preview only", "No capability was executed or activated"} {
		if !strings.Contains(string(body), want) { t.Fatalf("missing %q in body=%s", want, body) }
	}
}

func TestInvalidCollectionPreviewFailsClosed(t *testing.T) {
	s, _ := compositionServerFixture(t)
	server := httptest.NewServer(s.Handler("/mcp", 1<<20, AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer server.Close()
	response, err := http.Get(server.URL + "/ui/composition/preview?collection=missing")
	if err != nil { t.Fatal(err) }
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusBadRequest { t.Fatalf("status=%d body=%s", response.StatusCode, body) }
	if strings.Contains(string(body), "demo/repo/base") { t.Fatalf("unexpected dependency disclosure: %s", body) }
}

func compositionServerFixture(t *testing.T) (*Server, string) {
	t.Helper()
	index, err := search.New(nil)
	if err != nil { t.Fatal(err) }
	rootMetadata := map[string]string{
		composition.MetadataRequires: `[{"id":"demo/repo/base","version":"^1.0.0"}]`,
		composition.MetadataRecommends: `[{"id":"demo/repo/optional"}]`,
	}
	for _, doc := range []search.Document{
		{ID: "root-rev", SkillID: "demo/repo/root", OrganizationID: "demo", RepositoryID: "repo", Name: "root", Description: "root capability", Version: "1.0.0", Metadata: rootMetadata, Searchable: true, TrustLevel: "approved", Commit: "root-commit", Tree: "root-tree"},
		{ID: "base-rev", SkillID: "demo/repo/base", OrganizationID: "demo", RepositoryID: "repo", Name: "base", Description: "base capability", Version: "1.2.0", Searchable: true, TrustLevel: "approved", Commit: "base-commit", Tree: "base-tree"},
		{ID: "optional-rev", SkillID: "demo/repo/optional", OrganizationID: "demo", RepositoryID: "repo", Name: "optional", Description: "optional capability", Version: "1.0.0", Searchable: true, TrustLevel: "approved"},
	} {
		if err := index.Add(doc); err != nil { t.Fatal(err) }
	}
	scope, err := capability.NewScope("demo", "", "")
	if err != nil { t.Fatal(err) }
	capabilities, err := capability.New(index, []capability.SourcePolicy{{RepositoryID: "repo", Scope: scope}})
	if err != nil { t.Fatal(err) }
	s := NewWithSearch(nil, nil, index, "demo", candidate.Signer{Key: []byte("composition-candidate-key")})
	s.ConfigureCapabilities(capabilities)
	t.Cleanup(func() {
		s.ConfigureCapabilities(nil)
		configuredCompositionCollections.Delete(s)
	})
	now := time.Now()
	token, err := s.signer.Sign(candidate.Payload{Version: 1, OrganizationID: "demo", RevisionID: "root-rev", QueryID: "composition-test", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()})
	if err != nil { t.Fatal(err) }
	return s, token
}
