// Package distribution projects already-authorized, governed capability state
// into host-native distribution artifacts. It does not search, rank, execute,
// install, authenticate to source repositories, or mutate capability state.
package distribution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/mhingston/skillet/internal/capability"
)

const (
	ClaudeCodeProfile            = "claude-code-marketplace-v1"
	ClaudeCodeVerifiedVersion    = "2.1.270"
	ClaudeCodeMarketplaceSchema  = "https://anthropic.com/claude-code/marketplace.schema.json"
	MaxCapabilitiesPerArtifact   = 512
)

var (
	marketplaceNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	gitCommitPattern       = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)
)

// Capability is the bounded immutable projection required by a distribution
// adapter. Callers MUST authorize and scope-filter these values before passing
// them to BuildClaudeCodeMarketplace.
type Capability struct {
	ID                 string
	Name               string
	Description        string
	Version            string
	Status             capability.Status
	RepositoryURL      string
	Path               string
	RevisionID         string
	Commit             string
	Tree               string
	ArchiveSHA256TarGZ string
	ArchiveSHA256ZIP   string
}

type Warning struct {
	CapabilityID string `json:"capability_id,omitempty"`
	Code         string `json:"code"`
	Message      string `json:"message"`
}

type Entry struct {
	CapabilityID       string            `json:"capability_id"`
	PluginName         string            `json:"plugin_name"`
	CapabilityName     string            `json:"capability_name"`
	CapabilityVersion  string            `json:"capability_version,omitempty"`
	Status             capability.Status `json:"status"`
	RepositoryURL      string            `json:"repository_url"`
	Path               string            `json:"path"`
	RevisionID         string            `json:"revision_id"`
	Commit             string            `json:"commit"`
	Tree               string            `json:"tree,omitempty"`
	ArchiveSHA256TarGZ string            `json:"archive_sha256_tar_gz,omitempty"`
	ArchiveSHA256ZIP   string            `json:"archive_sha256_zip,omitempty"`
}

type Artifact struct {
	Profile         string    `json:"profile"`
	MarketplaceName string    `json:"marketplace_name"`
	Manifest        []byte    `json:"-"`
	ManifestSHA256  string    `json:"manifest_sha256"`
	Entries         []Entry   `json:"entries"`
	Warnings        []Warning `json:"warnings,omitempty"`
}

type claudeMarketplace struct {
	Schema      string         `json:"$schema"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Owner       claudeOwner    `json:"owner"`
	Plugins     []claudePlugin `json:"plugins"`
}

type claudeOwner struct {
	Name string `json:"name"`
}

type claudePlugin struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Source      claudeSource `json:"source"`
	Strict      bool         `json:"strict"`
	Skills      []string     `json:"skills"`
}

type claudeSource struct {
	Source string `json:"source"`
	URL    string `json:"url"`
	Path   string `json:"path"`
	SHA    string `json:"sha"`
}

// BuildClaudeCodeMarketplace deterministically renders the currently verified
// Claude Code marketplace contract. Invalid individual capabilities are
// isolated as warnings so one bad source cannot poison the rest of an already
// authorized catalogue snapshot. Yanked capabilities are never emitted.
func BuildClaudeCodeMarketplace(marketplaceName string, capabilities []Capability) (Artifact, error) {
	marketplaceName = strings.TrimSpace(marketplaceName)
	if !marketplaceNamePattern.MatchString(marketplaceName) {
		return Artifact{}, fmt.Errorf("invalid marketplace name %q", marketplaceName)
	}
	if len(capabilities) > MaxCapabilitiesPerArtifact {
		return Artifact{}, fmt.Errorf("capability snapshot exceeds limit")
	}

	values := append([]Capability(nil), capabilities...)
	sort.Slice(values, func(i, j int) bool {
		if values[i].ID != values[j].ID {
			return values[i].ID < values[j].ID
		}
		return values[i].RevisionID < values[j].RevisionID
	})

	plugins := make([]claudePlugin, 0, len(values))
	entries := make([]Entry, 0, len(values))
	warnings := []Warning{}
	seenRevision := map[string]bool{}
	seenPlugin := map[string]bool{}
	for _, item := range values {
		if item.Status == capability.StatusYanked {
			// Do not disclose yanked item metadata through a new-distribution
			// artifact. The generic warning is added below after filtering.
			continue
		}
		if item.Status != capability.StatusActive && item.Status != capability.StatusDeprecated {
			warnings = append(warnings, Warning{CapabilityID: item.ID, Code: "unsupported_status", Message: "capability was omitted because its governance state is not distributable"})
			continue
		}
		if seenRevision[item.RevisionID] {
			continue
		}
		if err := validateCapability(item); err != nil {
			warnings = append(warnings, Warning{CapabilityID: item.ID, Code: "invalid_source", Message: "capability was omitted because its immutable source cannot be represented by this host profile"})
			continue
		}
		pluginName := pluginNameFor(item)
		if seenPlugin[pluginName] {
			warnings = append(warnings, Warning{CapabilityID: item.ID, Code: "name_collision", Message: "capability was omitted because its host plugin name is not unique"})
			continue
		}
		seenRevision[item.RevisionID] = true
		seenPlugin[pluginName] = true

		description := cleanDisplayText(item.Description, 600)
		if item.Status == capability.StatusDeprecated {
			if description == "" {
				description = "DEPRECATED Skillet capability."
			} else {
				description = "DEPRECATED: " + description
			}
			warnings = append(warnings, Warning{CapabilityID: item.ID, Code: "deprecated", Message: "deprecated capability is included explicitly and is not upgraded or substituted"})
		}
		plugins = append(plugins, claudePlugin{
			Name:        pluginName,
			Description: description,
			Source: claudeSource{
				Source: "git-subdir",
				URL:    strings.TrimSpace(item.RepositoryURL),
				Path:   item.Path,
				SHA:    strings.ToLower(item.Commit),
			},
			Strict: false,
			Skills: []string{"./"},
		})
		entries = append(entries, Entry{
			CapabilityID: item.ID, PluginName: pluginName, CapabilityName: item.Name,
			CapabilityVersion: item.Version, Status: item.Status,
			RepositoryURL: strings.TrimSpace(item.RepositoryURL), Path: item.Path,
			RevisionID: item.RevisionID, Commit: strings.ToLower(item.Commit), Tree: item.Tree,
			ArchiveSHA256TarGZ: item.ArchiveSHA256TarGZ, ArchiveSHA256ZIP: item.ArchiveSHA256ZIP,
		})
	}
	if len(values) != 0 && len(plugins) < len(values) {
		warnings = append(warnings, Warning{Code: "filtered", Message: "one or more non-distributable or yanked capabilities were omitted from this snapshot"})
	}

	manifest := claudeMarketplace{
		Schema: ClaudeCodeMarketplaceSchema,
		Name: marketplaceName,
		Description: "Governed, read-only Skillet capability snapshot. Plugin sources are pinned to immutable Git commits.",
		Owner: claudeOwner{Name: "Skillet"},
		Plugins: plugins,
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Artifact{}, err
	}
	encoded = append(encoded, '\n')
	digest := sha256.Sum256(encoded)
	return Artifact{
		Profile: ClaudeCodeProfile, MarketplaceName: marketplaceName,
		Manifest: encoded, ManifestSHA256: hex.EncodeToString(digest[:]),
		Entries: entries, Warnings: warnings,
	}, nil
}

func validateCapability(item Capability) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.RevisionID) == "" {
		return fmt.Errorf("identity, name, and revision are required")
	}
	if !gitCommitPattern.MatchString(strings.TrimSpace(item.Commit)) {
		return fmt.Errorf("immutable git commit is required")
	}
	cleanPath := path.Clean(item.Path)
	if item.Path == "" || cleanPath != item.Path || cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, "../") || strings.HasPrefix(cleanPath, "/") || strings.Contains(item.Path, `\`) {
		return fmt.Errorf("safe skill path is required")
	}
	if strings.TrimSpace(item.RepositoryURL) == "" || !allowedGitURL(item.RepositoryURL) {
		return fmt.Errorf("supported git repository URL is required")
	}
	return nil
}

func allowedGitURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "git@") {
		return !strings.ContainsAny(raw, "\r\n\t ") && strings.Contains(raw, ":")
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" || u.User != nil {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "ssh"
}

func pluginNameFor(item Capability) string {
	base := slug(item.Name)
	if base == "" {
		base = slug(path.Base(item.Path))
	}
	if base == "" {
		base = "skill"
	}
	if len(base) > 48 {
		base = strings.Trim(base[:48], "-")
	}
	sum := sha256.Sum256([]byte(item.ID))
	return base + "-" + hex.EncodeToString(sum[:4])
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if r <= unicode.MaxASCII {
				b.WriteRune(r)
				lastDash = false
			}
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func cleanDisplayText(value string, limit int) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	space := false
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			if b.Len() > 0 {
				space = true
			}
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
		if b.Len() >= limit {
			break
		}
	}
	return strings.TrimSpace(b.String())
}
