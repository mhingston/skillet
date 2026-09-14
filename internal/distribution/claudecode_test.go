package distribution

import (
	"bytes"
	"encoding/json"
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
	if err != nil { t.Fatal(err) }
	second, err := BuildClaudeCodeMarketplace("skillet-org", []Capability{input[1], input[0]})
	if err != nil { t.Fatal(err) }
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
		Schema string `json:"$schema"`
		Name string `json:"name"`
		Plugins []struct {
			Name string `json:"name"`
			Description string `json:"description"`
			Strict bool `json:"strict"`
			Skills []string `json:"skills"`
			Source struct { Source, URL, Path, SHA string } `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(first.Manifest, &manifest); err != nil { t.Fatal(err) }
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
	if bytes.Contains(first.Manifest, []byte("<unsafe>")) == false {
		// JSON does not need HTML escaping for the host contract, but input must
		// remain data rather than changing JSON structure. Successful Unmarshal
		// above plus the pinned source assertions prove that boundary.
		t.Log("encoding/json HTML-escaped the display text as expected")
	}
}

func TestBuildClaudeCodeMarketplaceFiltersYankedAndIsolatesInvalidSources(t *testing.T) {
	items := []Capability{
		{ID: "org/repo/good", Name: "Good", Status: capability.StatusActive, RepositoryURL: "https://example.com/good.git", Path: "skills/good", RevisionID: "rev_good", Commit: strings.Repeat("1", 40)},
		{ID: "org/repo/yanked-secret", Name: "Secret Yanked", Status: capability.StatusYanked, RepositoryURL: "https://example.com/secret.git", Path: "skills/secret", RevisionID: "rev_secret", Commit: strings.Repeat("2", 40)},
		{ID: "org/repo/bad", Name: "Bad", Status: capability.StatusActive, RepositoryURL: "https://user:secret@example.com/bad.git", Path: "../bad", RevisionID: "rev_bad", Commit: "main"},
	}
	artifact, err := BuildClaudeCodeMarketplace("skillet-org", items)
	if err != nil { t.Fatal(err) }
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
	if first != pluginNameFor(base) { t.Fatal("plugin name must be stable") }
	if first == pluginNameFor(other) { t.Fatal("stable identities must disambiguate otherwise-equal names") }
	if !marketplaceNamePattern.MatchString(first) {
		t.Fatalf("plugin name is not kebab-case safe: %q", first)
	}
}
