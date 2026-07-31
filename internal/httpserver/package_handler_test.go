package httpserver

import (
	"context"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestPackageHandlerStreamsOnlyAuthorizedImmutableDigest(t *testing.T) {
	store := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	data := []byte("archive")
	digest, e := store.Put("zip", data)
	if e != nil {
		t.Fatal(e)
	}
	tok, e := (packageurl.Signer{Key: []byte("key")}).Sign(packageurl.Payload{Version: 1, OrganizationID: "demo", Digest: digest, Format: "zip", ExpiresAt: time.Now().Add(time.Minute).Unix()})
	if e != nil {
		t.Fatal(e)
	}
	h := PackageHandler(store, packageurl.Signer{Key: []byte("key")}, "demo")
	r := httptest.NewRequest("GET", "/v1/packages/"+digest+".zip?token="+tok, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || string(w.Body.Bytes()) != "archive" {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, httptest.NewRequest("GET", "/v1/packages/"+digest+".zip?token=bad", nil))
	if bad.Code != 401 {
		t.Fatalf("bad status=%d", bad.Code)
	}
}

func TestPackageHandlerUsesAuthenticatedOrganization(t *testing.T) {
	store := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	digest, err := store.Put("zip", []byte("package"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := (packageurl.Signer{Key: []byte("key")}).Sign(packageurl.Payload{Version: 1, OrganizationID: "tenant-b", Digest: digest, Format: "zip", ExpiresAt: time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	h := PackageHandler(store, packageurl.Signer{Key: []byte("key")}, "tenant-a")
	req := httptest.NewRequest("GET", "/v1/packages/"+digest+".zip?token="+token, nil)
	req = req.WithContext(context.WithValue(req.Context(), organizationContextKey{}, "tenant-b"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s; authenticated organization was ignored", w.Code, w.Body.String())
	}
}

func TestPackageHandlerCacheLifetimeMatchesURLTTL(t *testing.T) {
	store := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	digest, err := store.Put("zip", []byte("package"))
	if err != nil {
		t.Fatal(err)
	}
	signer := packageurl.Signer{Key: []byte("key")}
	token, err := signer.Sign(packageurl.Payload{Version: 1, OrganizationID: "demo", Digest: digest, Format: "zip", ExpiresAt: time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	h := PackageHandlerWithMetricsAndAuditTTL(store, signer, "demo", nil, nil, 17*time.Second)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/packages/"+digest+".zip?token="+token, nil))
	if rr.Code != http.StatusOK || rr.Header().Get("Cache-Control") != "private, max-age=17, immutable" {
		t.Fatalf("status=%d cache-control=%q", rr.Code, rr.Header().Get("Cache-Control"))
	}
}
