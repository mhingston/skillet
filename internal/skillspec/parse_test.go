package skillspec

import (
	"strings"
	"testing"
)

func TestParseAndValidateFrontmatter(t *testing.T) {
	doc, err := Parse([]byte("---\nname: plan\ndescription: Make an evidence-grounded plan.\nlicense: MIT\ncompatibility: Go repositories\nmetadata:\n  intent: planning\nallowed-tools: \"\"\n---\n\n# Plan\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if doc.Frontmatter.Name != "plan" || doc.Frontmatter.Description == "" {
		t.Fatalf("unexpected frontmatter: %+v", doc.Frontmatter)
	}
	if doc.Frontmatter.Metadata["intent"] != "planning" {
		t.Fatalf("metadata was not preserved: %+v", doc.Frontmatter.Metadata)
	}
	if doc.Body != "\n# Plan\n" {
		t.Fatalf("body = %q", doc.Body)
	}
	if findings := Validate(doc.Frontmatter); len(findings) != 0 {
		t.Fatalf("valid document findings = %+v", findings)
	}
}

func TestParseRejectsMissingOrMalformedFrontmatter(t *testing.T) {
	for name, input := range map[string][]byte{
		"missing opening delimiter": []byte("name: plan\n---\n"),
		"missing closing delimiter": []byte("---\nname: plan\ndescription: x\n"),
		"invalid yaml":              []byte("---\nname: [\n---\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(input); err == nil {
				t.Fatal("Parse() error = nil")
			}
		})
	}
}

func TestValidateReportsSpecificationFindings(t *testing.T) {
	findings := Validate(Frontmatter{
		Name:        "Bad Name",
		Description: strings.Repeat("x", 1025),
		Metadata:    map[string]string{"bad": ""},
	})
	if !hasFinding(findings, FindingMissingDescription) && len(findings) == 0 {
		t.Fatal("Validate() returned no findings")
	}
	if !hasFinding(findings, FindingInvalidName) {
		t.Fatalf("findings = %+v; missing invalid-name finding", findings)
	}
	if !hasFinding(findings, FindingDescriptionTooLong) {
		t.Fatalf("findings = %+v; missing description-length finding", findings)
	}
}

func hasFinding(findings []Finding, code FindingCode) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
