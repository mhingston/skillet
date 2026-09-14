package catalogue

import "testing"

func TestRedactOperatorURLRemovesCredentialsAndQuery(t *testing.T) {
	got := redactOperatorURL("https://user:secret@example.invalid/repo.git?token=query-secret#fragment")
	if got != "https://example.invalid/repo.git" {
		t.Fatalf("redacted URL = %q", got)
	}
}

func TestRedactOperatorURLPreservesCredentialFreeSource(t *testing.T) {
	got := redactOperatorURL("file:///srv/skills")
	if got != "file:///srv/skills" {
		t.Fatalf("redacted URL = %q", got)
	}
}
