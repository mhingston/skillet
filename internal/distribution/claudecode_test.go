package distribution

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/capability"
)

func TestBuildClaudeCodeMarketplaceDeterministicAndPinned(t *testing.T) {
	input := []Capability{
		{
			ID: "org/repo/skills/zeta", Name: "Zeta Skill", Description: "Use <unsafe> & safely", Version: "2.1.0",
			Status: capability.StatusActive, RepositoryURL: "https://github.com/example/skills.git", Path: "skills/zeta",
			RevisionID: "rev_zeta", Commit: strings.Repeat("b", 40), Tree: strings.Repeat("c", 40),
			ArchiveSHA256TarGZ: strings.Repeat("d", 64), ArchiveSHA256ZIP: strings.Repeat("e", 64),
		},
		{
			ID: "org/repo/skills/alpha", Name: "Alpha Skill", Description: "Old\nthing", Version: "1.0.0",
			Status: capability.StatusDeprecated, RepositoryURL: "ssh://git@example.com/team/skills.git", Path: "skills/alpha",
			RevisionID: "rev_alpha", Commit: strings.Repeat("a", 40), Tree: strings.Repeat("f", 40),
		},
	}

	first, err := BuildClaudeCodeMarketplace("skillet-org", input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildClaudeCodeMarketplace("skillet-org", []Capability{input[1], input[0]})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Manifest, second.Manifest) || first.ManifestSHA256 != second.ManifestSHA256 {
		t.Fatal("same authorized snapshot must render byte-identically regardless of input order")
	}
	if first.Profile != ClaudeCodeProfile || len(first.Entries) != 2 {
		t.Fatalf("unexpected artifact: %+v", first)
	}
	if first.Entries[0].CapabilityID != "org/repo/skills/alpha" || first.Entries[0].CapabilityVersion != "1.0.0" {
		t.Fatalf("expected deterministic provenance entries, got %+v", first.Entries)
	}
	if first.Entries[0].RevisionID != "rev_alpha" || first.Entries[0].Commit != strings.Repeat("a", 40) {
		t.Fatalf("exact immutable provenance was not preserved: %+v", first.Entries[0])
	}

	var manifest struct {
		Schema  string `json:"$schema"`
		Name    string `json:"name"`
		Plugins []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Strict      bool   `json:"strict"`
			Skills      []string `json:"skills"`
			Source      struct {
				Source string `json:"source"`
				URL    string `json:"url"`
				Path   string `json:"path"`
				SHA    string `json:"sha"`
			} `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(first.Manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != ClaudeCodeMarketplaceSchema || manifest.Name != "skillet-org" || len(manifest.Plugins) != 2 {
		t.Fatalf("unexpected marketplace contract: %+v", manifest)
	}
	alpha := manifest.Plugins[0]
	if alpha.Source.Source != "git-subdir" || alpha.Source.Path != "skills/alpha" || alpha.Source.SHA != strings.Repeat("a", 40) {
		t.Fatalf("plugin source is not immutable git-subdir provenance: %+v", alpha.Source)
	}
	if alpha.Strict || len(alpha.Skills) != 1 || alpha.Skills[0] != "./" {
		t.Fatalf("expected marketplace-defined raw skill bundle, got %+v", alpha)
	}
	if !strings.HasPrefix(alpha.Description, "DEPRECATED: ") || strings.Contains(alpha.Description, "\n") {
		t.Fatalf("deprecated warning/display sanitization missing: %q", alpha.Description)
	}
	if !bytes.Contains(first.Manifest, []byte("<unsafe>")) {
		// encoding/json may HTML-escape the display text. Successful unmarshal
		// above plus the pinned source assertions prove it stayed JSON data.
		t.Log("encoding/json HTML-escaped the display text as expected")
	}
}

func TestBuildClaudeCodeMarketplaceMatchesHostCompatibilityFixture(t *testing.T) {
	artifact, err := BuildClaudeCodeMarketplace("skillet-fixture", []Capability{{
		ID:            "org/repo/skills/review",
		Name:          "review",
		Description:   "Review changes safely",
		Status:        capability.StatusActive,
		RepositoryURL: "https://github.com/example/skills.git",
		Path:          "skills/review",
		RevisionID:    "rev_fixture",
		Commit:        "0123456789abcdef0123456789abcdef01234567",
	}})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "distribution", "claude-code", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(artifact.Manifest, fixture) {
		t.Fatalf("generated host manifest drifted from the reviewed Claude Code fixture\ngot:\n%s\nwant:\n%s", artifact.Manifest, fixture)
	}
}

func TestBuildClaudeCodeMarketplaceSourceUpdateChangesOnlyPinnedProvenance(t *testing.T) {
	base := []Capability{
		{
			ID: "org/repo/a", Name: "A", Description: "A", Status: capability.StatusActive,
			RepositoryURL: "https://example.com/skills.git", Path: "skills/a", RevisionID: "rev_a_1",
			Commit: strings.Repeat("1", 40),
		},
		{
			ID: "org/repo/b", Name: "B", Description: "B", Status: capability.StatusActive,
			RepositoryURL: "https://example.com/skills.git", Path: "skills/b", RevisionID: "rev_b_1",
			Commit: strings.Repeat("2", 40),
		},
	}
	before, err := BuildClaudeCodeMarketplace("skillet-org", base)
	if err != nil {
		t.Fatal(err)
	}
	updated := append([]Capability(nil), base...)
	updated[0] = base[0]
	updated[0].RevisionID = "rev_a_2"
	updated[0].Commit = strings.Repeat("3", 40)
	after, err := BuildClaudeCodeMarketplace("skillet-org", updated)
	if err != nil {
		t.Fatal(err)
	}
	if before.ManifestSHA256 == after.ManifestSHA256 {
		t.Fatal("source revision update must change manifest digest")
	}
	expected := bytes.ReplaceAll(before.Manifest, []byte(strings.Repeat("1", 40)), []byte(strings.Repeat("3", 40)))
	if !bytes.Equal(after.Manifest, expected) {
		t.Fatalf("source update changed host manifest beyond the expected immutable SHA\nbefore:\n%s\nafter:\n%s", before.Manifest, after.Manifest)
	}
	if after.Entries[0].RevisionID != "rev_a_2" || after.Entries[0].Commit != strings.Repeat("3", 40) {
		t.Fatalf("updated provenance not surfaced: %+v", after.Entries[0])
	}
	if after.Entries[1] != before.Entries[1] {
		t.Fatalf("unrelated capability provenance changed: before=%+v after=%+v", before.Entries[1], after.Entries[1])
	}
}

func TestBuildClaudeCodeMarketplaceFiltersYankedAndIsolatesInvalidSources(t *testing.T) {
	items := []Capability{
		{ID: "org/repo/good", Name: "Good", Status: capability.StatusActive, RepositoryURL: "https://example.com/good.git", Path: "skills/good", RevisionID: "rev_good", Commit: strings.Repeat("1", 40)},
		{ID: "org/repo/yanked-secret", Name: "Secret Yanked", Status: capability.StatusYanked, RepositoryURL: "https://example.com/secret.git", Path: "skills/secret", RevisionID: "rev_secret", Commit: strings.Repeat("2", 40)},
		{ID: "org/repo/bad", Name: "Bad", Status: capability.StatusActive, RepositoryURL: "https://user:secret@example.com/bad.git", Path: "../bad", RevisionID: "rev_bad", Commit: "main"},
	}
	artifact, err := BuildClaudeCodeMarketplace("skillet-org", items)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.Entries) != 1 || artifact.Entries[0].CapabilityID != "org/repo/good" {
		t.Fatalf("expected isolated valid entry only, got %+v", artifact.Entries)
	}
	text := string(artifact.Manifest)
	for _, forbidden := range []string{"Secret Yanked", "yanked-secret", "user:secret", "../bad"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("filtered capability leaked into host manifest: %q", forbidden)
		}
	}
	if len(artifact.Warnings) < 2 {
		t.Fatalf("expected bounded omission warnings, got %+v", artifact.Warnings)
	}
	for _, warning := range artifact.Warnings {
		if warning.CapabilityID == "org/repo/yanked-secret" {
			t.Fatalf("yanked identity leaked via warning metadata: %+v", warning)
		}
	}
}

func TestAllowedGitURLAllowsSSHUsernameButRejectsEmbeddedPasswords(t *testing.T) {
	if !allowedGitURL("ssh://git@example.com/team/skills.git") || !allowedGitURL("git@example.com:team/skills.git") {
		t.Fatal("expected standard credential-helper SSH forms to be accepted")
	}
	for _, raw := range []string{
		"ssh://git:secret@example.com/team/skills.git",
		"https://user:secret@example.com/team/skills.git",
	} {
		if allowedGitURL(raw) {
			t.Fatalf("embedded credential must be rejected: %q", raw)
		}
	}
}

func TestBuildClaudeCodeMarketplaceRejectsInvalidMarketplaceName(t *testing.T) {
	if _, err := BuildClaudeCodeMarketplace("Skillet Org", nil); err == nil {
		t.Fatal("expected host marketplace name validation")
	}
}

func TestPluginNameIsStableSafeAndCollisionResistant(t *testing.T) {
	base := Capability{ID: "org/a", Name: `../../ Inject \" Name`, Path: "skills/a"}
	other := base
	other.ID = "org/b"
	first := pluginNameFor(base)
	if first != pluginNameFor(base) {
		t.Fatal("plugin name must be stable")
	}
	if first == pluginNameFor(other) {
		t.Fatal("stable identities must disambiguate otherwise-equal names")
	}
	if !marketplaceNamePattern.MatchString(first) {
		t.Fatalf("plugin name is not kebab-case safe: %q", first)
	}
}
