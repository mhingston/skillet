package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	authn "github.com/mhingston/skillet/internal/auth"
	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/packageurl"
)

func TestClaimsPackageDownloadRequiresSignedRevisionIdentity(t *testing.T) {
	s, first, _ := lockedMaterializeFixture(t)
	policy, err := authz.NewClaimsPolicy([]authz.Grant{{
		Permissions: []string{"capability.materializer"},
		Actions:     []authz.Action{authz.ActionCapabilityMaterialize},
		Resources:   []authz.ResourceRule{{IDs: []string{first.SkillID}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureAuthorization(policy)
	t.Cleanup(func() { s.ConfigureAuthorization(nil) })

	validator, err := authn.NewStaticBearerValidator("secret", "demo", []string{"capability.materializer"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := validator.Authenticate("Bearer secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx := withAuthenticatedIdentity(context.Background(), identity)
	input := materializeInput{Locked: &lockedInput{
		SkillID:       first.SkillID,
		RepositoryID:  "skills",
		Path:          first.Path,
		Commit:        first.Commit,
		Tree:          first.Tree,
		ArchiveSHA256: first.ArchiveSHA256TarGZ,
		Format:        "tar.gz",
	}}
	_, out, err := s.authorizedMaterializeTool(ctx, nil, input)
	if err != nil {
		t.Fatal(err)
	}
	boundURL, err := url.Parse(out.Package.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := s.packageSigner.Verify(boundURL.Query().Get("token"), "demo", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if payload.RevisionID != first.RevisionID {
		t.Fatalf("package token revision = %q, want %q", payload.RevisionID, first.RevisionID)
	}

	handler := s.Handler("/mcp", 1<<20, AuthConfig{Mode: "static", StaticToken: "secret", OrganizationID: "demo", Validator: validator})
	request := func(rawURL string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, rawURL, nil)
		req.Header.Set("Authorization", "Bearer secret")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}
	if rr := request(out.Package.DownloadURL); rr.Code != http.StatusOK {
		t.Fatalf("revision-bound package status = %d, body=%s", rr.Code, rr.Body.String())
	}

	legacyToken, err := s.packageSigner.Sign(packageurl.Payload{
		Version:        1,
		OrganizationID: "demo",
		Digest:         first.ArchiveSHA256TarGZ,
		Format:         "tar.gz",
		ExpiresAt:      time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyURL := *boundURL
	query := legacyURL.Query()
	query.Set("token", legacyToken)
	legacyURL.RawQuery = query.Encode()
	if rr := request(legacyURL.String()); rr.Code != http.StatusUnauthorized {
		t.Fatalf("digest-only package status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}
