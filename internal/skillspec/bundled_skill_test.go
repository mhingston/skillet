package skillspec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundledFindSkillsSkillIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "skills", "find-skills", "SKILL.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bundled find-skills skill: %v", err)
	}

	doc, err := Parse(content)
	if err != nil {
		t.Fatalf("parse bundled find-skills skill: %v", err)
	}
	if doc.Frontmatter.Name != "find-skills" {
		t.Fatalf("name = %q, want find-skills", doc.Frontmatter.Name)
	}
	if findings := Validate(doc.Frontmatter); len(findings) != 0 {
		t.Fatalf("bundled find-skills skill has validation findings: %+v", findings)
	}
}
