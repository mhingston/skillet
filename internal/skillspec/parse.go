// Package skillspec contains the local, side-effect-free Agent Skills
// frontmatter contract used by repository admission.
package skillspec

import (
	"fmt"
	"regexp"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

const (
	MaxNameLength        = 64
	MaxDescriptionLength = 1024
	MaxCompatibilityLen  = 500
)

type Frontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
	AllowedTools  string            `yaml:"allowed-tools,omitempty"`
}

type Document struct {
	Frontmatter Frontmatter
	Body        string
}

type FindingCode string

const (
	FindingMalformedFrontmatter  FindingCode = "malformed_frontmatter"
	FindingMissingName           FindingCode = "missing_name"
	FindingInvalidName           FindingCode = "invalid_name"
	FindingNameTooLong           FindingCode = "name_too_long"
	FindingMissingDescription    FindingCode = "missing_description"
	FindingDescriptionTooLong    FindingCode = "description_too_long"
	FindingCompatibilityLong     FindingCode = "compatibility_too_long"
	FindingNameDirectoryMismatch FindingCode = "name_directory_mismatch"
	FindingInvalidVersion        FindingCode = "invalid_version"
)

type Finding struct {
	Code    FindingCode
	Message string
}

var validName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func ValidName(name string) bool {
	return name != "" && len(name) <= MaxNameLength && validName.MatchString(name)
}

// Parse parses a complete SKILL.md and returns frontmatter and the untouched
// body. It intentionally rejects unknown top-level YAML keys: extensions can
// be represented inside metadata without becoming accidental standard fields.
func Parse(content []byte) (Document, error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Document{}, fmt.Errorf("SKILL.md must begin with YAML frontmatter delimiter")
	}
	rest := text[len("---\n"):]
	close := strings.Index(rest, "\n---\n")
	separatorLen := len("\n---\n")
	if close < 0 && strings.HasSuffix(rest, "\n---") {
		close = len(rest) - len("\n---")
		separatorLen = len("\n---")
	}
	if close < 0 {
		return Document{}, fmt.Errorf("SKILL.md frontmatter has no closing delimiter")
	}
	frontmatterText := rest[:close]
	body := rest[close+separatorLen:]

	var raw map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(frontmatterText), &raw); err != nil {
		return Document{}, fmt.Errorf("parse SKILL.md frontmatter: %w", err)
	}
	for key := range raw {
		switch key {
		case "name", "description", "license", "compatibility", "metadata", "allowed-tools":
		default:
			return Document{}, fmt.Errorf("unsupported SKILL.md frontmatter field %q", key)
		}
	}
	var fm Frontmatter
	if err := yaml.Unmarshal([]byte(frontmatterText), &fm); err != nil {
		return Document{}, fmt.Errorf("decode SKILL.md frontmatter: %w", err)
	}
	return Document{Frontmatter: fm, Body: body}, nil
}

func Validate(fm Frontmatter) []Finding {
	var findings []Finding
	if fm.Name == "" {
		findings = append(findings, Finding{FindingMissingName, "name is required"})
	} else if len(fm.Name) > MaxNameLength {
		findings = append(findings, Finding{FindingNameTooLong, fmt.Sprintf("name must be at most %d characters", MaxNameLength)})
	} else if !validName.MatchString(fm.Name) {
		findings = append(findings, Finding{FindingInvalidName, "name must contain lowercase letters, numbers, and single hyphens only"})
	}
	if fm.Description == "" {
		findings = append(findings, Finding{FindingMissingDescription, "description is required"})
	} else if len(fm.Description) > MaxDescriptionLength {
		findings = append(findings, Finding{FindingDescriptionTooLong, fmt.Sprintf("description must be at most %d characters", MaxDescriptionLength)})
	}
	if len(fm.Compatibility) > MaxCompatibilityLen {
		findings = append(findings, Finding{FindingCompatibilityLong, fmt.Sprintf("compatibility must be at most %d characters", MaxCompatibilityLen)})
	}
	if value, ok := fm.Metadata["version"]; ok {
		if _, err := semver.StrictNewVersion(value); err != nil {
			findings = append(findings, Finding{FindingInvalidVersion, fmt.Sprintf("metadata.version must be valid SemVer 2.0: %v", err)})
		}
	}
	return findings
}
