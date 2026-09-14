package proposal

import (
	"strings"
	"testing"
)

func TestValidatePatchIsBoundedToRecordedCapabilitySource(t *testing.T) {
	valid := strings.Join([]string{
		"diff --git a/skills/release/SKILL.md b/skills/release/SKILL.md",
		"--- a/skills/release/SKILL.md",
		"+++ b/skills/release/SKILL.md",
		"@@ -1 +1 @@",
		"-old",
		"+new",
	}, "\n")
	if err := ValidatePatch("skills/release", valid); err != nil {
		t.Fatalf("valid scoped patch rejected: %v", err)
	}
	outside := strings.ReplaceAll(valid, "skills/release/SKILL.md", "README.md")
	if err := ValidatePatch("skills/release", outside); err == nil {
		t.Fatal("patch outside capability source path was accepted")
	}
	binary := valid + "\nGIT binary patch\n"
	if err := ValidatePatch("skills/release", binary); err == nil {
		t.Fatal("binary patch was accepted")
	}
	symlink := valid + "\nnew file mode 120000\n"
	if err := ValidatePatch("skills/release", symlink); err == nil {
		t.Fatal("symlink patch was accepted")
	}
}

func TestVerificationCannotOmitFailOrInventObligations(t *testing.T) {
	obligations := []VerificationObligation{
		{Name: "source_ingestion_validation"},
		{Name: "relevant_regression_evals"},
	}
	passing := []VerificationResult{
		{Name: "source_ingestion_validation", Command: "verify source", Passed: true, ExitCode: 0},
		{Name: "relevant_regression_evals", Command: "run evals", Passed: true, ExitCode: 0},
	}
	clean, ready, err := validateVerification(obligations, passing)
	if err != nil || !ready || len(clean) != 2 {
		t.Fatalf("passing verification = ready %v clean=%+v err=%v", ready, clean, err)
	}
	failed := append([]VerificationResult(nil), passing...)
	failed[1].Passed = false
	failed[1].ExitCode = 1
	if _, ready, err := validateVerification(obligations, failed); err != nil || ready {
		t.Fatalf("failed verification became ready: ready=%v err=%v", ready, err)
	}
	if _, ready, err := validateVerification(obligations, passing[:1]); err != nil || ready {
		t.Fatalf("omitted obligation became ready: ready=%v err=%v", ready, err)
	}
	invented := append([]VerificationResult(nil), passing...)
	invented = append(invented, VerificationResult{Name: "lower_threshold", Command: "edit threshold", Passed: true, ExitCode: 0})
	if _, _, err := validateVerification(obligations, invented); err == nil {
		t.Fatal("invented verification obligation was accepted")
	}
}

func TestSourceRedactionRemovesCommonCredentialForms(t *testing.T) {
	input := strings.Join([]string{
		"Authorization: Bearer top-secret-token",
		"api_key=abc123",
		"client_secret: hunter2",
		"ordinary=value",
	}, "\n")
	got, count := redactText(input)
	if count != 3 {
		t.Fatalf("redaction count=%d, want 3: %q", count, got)
	}
	for _, secret := range []string{"top-secret-token", "abc123", "hunter2"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted output leaked %q: %q", secret, got)
		}
	}
	if !strings.Contains(got, "ordinary=value") {
		t.Fatalf("ordinary content was changed: %q", got)
	}
}
