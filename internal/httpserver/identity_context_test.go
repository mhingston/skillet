package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	authn "github.com/mhingston/skillet/internal/auth"
)

func TestAuthMiddlewarePreservesTrustedValidatorIdentity(t *testing.T) {
	validator, err := authn.NewStaticBearerValidator("secret", "demo", []string{"skills.search"})
	if err != nil {
		t.Fatal(err)
	}

	var observed authn.Identity
	var observedOrganization string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		observed, ok = Identity(r.Context())
		if !ok {
			t.Fatal("trusted identity missing from request context")
		}
		observedOrganization, ok = OrganizationID(r.Context())
		if !ok {
			t.Fatal("organization missing from request context")
		}
		w.WriteHeader(http.StatusNoContent)
	})

	h := authMiddleware(next, AuthConfig{Mode: "static", OrganizationID: "demo", Validator: validator})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if observed.Subject != "static" || observed.OrganizationID != "demo" || !observed.HasScope("skills.search") {
		t.Fatalf("identity = %+v", observed)
	}
	if observedOrganization != observed.OrganizationID {
		t.Fatalf("organization = %q, identity organization = %q", observedOrganization, observed.OrganizationID)
	}
}

func TestAuthMiddlewareLegacyStaticPathSynthesizesIdentity(t *testing.T) {
	var observed authn.Identity
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		observed, ok = Identity(r.Context())
		if !ok {
			t.Fatal("static identity missing from request context")
		}
		w.WriteHeader(http.StatusNoContent)
	})

	h := authMiddleware(next, AuthConfig{Mode: "static", StaticToken: "secret", OrganizationID: "demo"})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if observed.Subject != "static" || observed.OrganizationID != "demo" {
		t.Fatalf("identity = %+v", observed)
	}
}

func TestDevelopmentModeDoesNotInventTrustedIdentity(t *testing.T) {
	if _, ok := Identity(httptest.NewRequest(http.MethodGet, "/", nil).Context()); ok {
		t.Fatal("identity unexpectedly present")
	}
}
