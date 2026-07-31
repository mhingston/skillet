package candidate

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
	RevisionID     string `json:"revision_id"`
	QueryID        string `json:"query_id"`
	IssuedAt       int64  `json:"issued_at"`
	ExpiresAt      int64  `json:"expires_at"`
}
type Signer struct {
	Key []byte
	TTL time.Duration
}

func (s Signer) Sign(p Payload) (string, error) {
	if len(s.Key) == 0 {
		return "", fmt.Errorf("candidate signing key is required")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.Key)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (s Signer) Verify(token, organizationID string, now time.Time) (Payload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Payload{}, fmt.Errorf("invalid candidate token")
	}
	mac := hmac.New(sha256.New, s.Key)
	_, _ = mac.Write([]byte(parts[0]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return Payload{}, fmt.Errorf("invalid candidate signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Payload{}, fmt.Errorf("invalid candidate payload")
	}
	var p Payload
	if err = json.Unmarshal(raw, &p); err != nil || p.Version != 1 || p.OrganizationID != organizationID || p.RevisionID == "" || p.QueryID == "" || p.ExpiresAt <= p.IssuedAt || p.ExpiresAt <= now.Unix() || p.IssuedAt > now.Add(5*time.Minute).Unix() {
		return Payload{}, fmt.Errorf("expired or unauthorized candidate")
	}
	return p, nil
}
