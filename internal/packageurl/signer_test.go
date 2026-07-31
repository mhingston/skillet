package packageurl

import (
	"testing"
	"time"
)

func TestSignerBindsDigestFormatOrgAndExpiry(t *testing.T) {
	s := Signer{Key: []byte("key")}
	tok, e := s.Sign(Payload{Version: 1, OrganizationID: "demo", Digest: "abc", Format: "zip", ExpiresAt: 100})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Verify(tok, "demo", time.Unix(99, 0)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Verify(tok, "demo", time.Unix(101, 0)); e == nil {
		t.Fatal("expired token accepted")
	}
	if _, e = s.Verify(tok, "other", time.Unix(99, 0)); e == nil {
		t.Fatal("cross-org token accepted")
	}
}
