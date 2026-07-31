package discovery

import (
	"errors"
	"testing"

	"github.com/mhingston/skillet/internal/skillspec"
)

func TestDiscoverFindsNestedSkillsAndAppliesPathPolicies(t *testing.T) {
	entries := []TreeEntry{
		{Path: "public/plan/SKILL.md", Kind: RegularFile},
		{Path: "public/plan/references/checklist.md", Kind: RegularFile},
		{Path: "internal/hidden/SKILL.md", Kind: RegularFile},
		{Path: ".git/config", Kind: RegularFile},
	}
	got, err := Discover(entries, func(path string) ([]byte, error) {
		if path == "public/plan/SKILL.md" {
			return []byte("---\nname: plan\ndescription: Planning\n---\n"), nil
		}
		if path == "internal/hidden/SKILL.md" {
			return []byte("---\nname: hidden\ndescription: Internal\nmetadata:\n  internal: \"true\"\n---\n"), nil
		}
		return nil, errors.New("must not read non-SKILL.md files")
	}, Options{
		Include:          []string{"**/SKILL.md"},
		Exclude:          []string{"internal/**", ".git/**"},
		SearchExclusions: []MetadataRule{{Key: "internal", Equals: "true"}},
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 || got[0].RelativePath != "public/plan" || got[0].Searchable != true {
		t.Fatalf("discoveries = %+v", got)
	}
	if got[0].Entrypoint != "public/plan/SKILL.md" {
		t.Fatalf("entrypoint = %q", got[0].Entrypoint)
	}
}

func TestDiscoverRejectsUnsafeEntriesAndSymlinks(t *testing.T) {
	for name, entries := range map[string][]TreeEntry{
		"parent traversal": {{Path: "../escape/SKILL.md", Kind: RegularFile}},
		"absolute path":    {{Path: "/escape/SKILL.md", Kind: RegularFile}},
		"symlink":          {{Path: "skill/SKILL.md", Kind: Symlink}},
		"special file":     {{Path: "skill/SKILL.md", Kind: Device}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Discover(entries, func(string) ([]byte, error) {
				return []byte("---\nname: x\ndescription: x\n---\n"), nil
			}, Options{}); err == nil {
				t.Fatal("Discover() error = nil")
			}
		})
	}
}

func TestDiscoverDoesNotPartiallyReturnOnReadOrParseFailure(t *testing.T) {
	entries := []TreeEntry{
		{Path: "a/SKILL.md", Kind: RegularFile},
		{Path: "b/SKILL.md", Kind: RegularFile},
	}
	got, err := Discover(entries, func(path string) ([]byte, error) {
		if path == "b/SKILL.md" {
			return nil, errors.New("read failed")
		}
		return []byte("---\nname: a\ndescription: A\n---\n"), nil
	}, Options{})
	if err == nil || got != nil {
		t.Fatalf("Discover() = (%+v, %v), want atomic failure", got, err)
	}
}

func TestDiscoverQuarantinesMalformedSkillWithoutIndexingIt(t *testing.T) {
	got, err := Discover([]TreeEntry{{Path: "broken/SKILL.md", Kind: RegularFile}}, func(string) ([]byte, error) {
		return []byte("---\nname: [\n---\n"), nil
	}, Options{})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 || got[0].State != Quarantined || got[0].Searchable || len(got[0].Findings) != 1 {
		t.Fatalf("discoveries = %+v", got)
	}
}

func TestDiscoverMetadataRuleHidesOtherwiseValidSkill(t *testing.T) {
	got, err := Discover([]TreeEntry{{Path: "internal/SKILL.md", Kind: RegularFile}}, func(string) ([]byte, error) {
		return []byte("---\nname: internal\ndescription: Internal helper\nmetadata:\n  internal: \"true\"\n---\n"), nil
	}, Options{SearchExclusions: []MetadataRule{{Key: "internal", Equals: "true"}}})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 || got[0].State != Admitted || got[0].Searchable {
		t.Fatalf("discoveries = %+v", got)
	}
}

func TestDiscoverQuarantinesNameDirectoryMismatch(t *testing.T) {
	got, err := Discover([]TreeEntry{{Path: "plan/SKILL.md", Kind: RegularFile}}, func(string) ([]byte, error) {
		return []byte("---\nname: review\ndescription: Review\n---\n"), nil
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].State != Quarantined || !hasFinding(got[0].Findings, skillspec.FindingNameDirectoryMismatch) {
		t.Fatalf("discoveries = %+v", got)
	}
}

func hasFinding(findings []skillspec.Finding, code skillspec.FindingCode) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
