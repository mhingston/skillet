package candidate

import (
	"testing"
	"time"
)

func TestSignerBindsOrganisationAndExpiry(t *testing.T) {
	s := Signer{Key: []byte("secret"), TTL: time.Minute}
	now := time.Unix(100, 0)
	token, err := s.Sign(Payload{Version: 1, OrganizationID: "demo", RevisionID: "rev", QueryID: "q", IssuedAt: 100, ExpiresAt: 160})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(token, "demo", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(token, "other", now); err == nil {
		t.Fatal("cross-org token accepted")
	}
	if _, err := s.Verify(token, "demo", time.Unix(161, 0)); err == nil {
		t.Fatal("expired token accepted")
	}
}
