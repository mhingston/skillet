package packageurl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Payload struct {
	Version        int    `json:"version"`
	OrganizationID string `json:"organization_id"`
	Digest         string `json:"digest"`
	Format         string `json:"format"`
	ExpiresAt      int64  `json:"expires_at"`
}
type Signer struct{ Key []byte }

func (s Signer) Sign(p Payload) (string, error) {
	if len(s.Key) == 0 {
		return "", fmt.Errorf("package signing key is required")
	}
	b, e := json.Marshal(p)
	if e != nil {
		return "", e
	}
	a := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, s.Key)
	_, _ = m.Write([]byte(a))
	return a + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}
func (s Signer) Verify(token, org string, now time.Time) (Payload, error) {
	if len(s.Key) == 0 {
		return Payload{}, fmt.Errorf("package signing key is required")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Payload{}, fmt.Errorf("invalid package token")
	}
	m := hmac.New(sha256.New, s.Key)
	_, _ = m.Write([]byte(parts[0]))
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || !hmac.Equal(sig, m.Sum(nil)) {
		return Payload{}, fmt.Errorf("invalid package signature")
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return Payload{}, fmt.Errorf("invalid package payload")
	}
	var p Payload
	if e = json.Unmarshal(b, &p); e != nil || p.Version != 1 || p.OrganizationID != org || p.ExpiresAt <= now.Unix() {
		return Payload{}, fmt.Errorf("expired or unauthorized package token")
	}
	return p, nil
}
