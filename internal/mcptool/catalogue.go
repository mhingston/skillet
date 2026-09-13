// Package mcptool ingests deterministic MCP tool metadata snapshots into the
// capability discovery domain. It deliberately has no MCP client, credential,
// or tool-execution dependency.
package mcptool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/search"
)

const (
	SnapshotVersion       = 1
	MaxSnapshotBytes      = 1 << 20
	MaxSchemaBytes        = 64 << 10
	MaxToolsPerSnapshot   = 1000
	MaxDescriptionBytes   = 4096
	maxSchemaSummaryBytes = 512
)

type Snapshot struct {
	Version int    `json:"version"`
	Server  Server `json:"server"`
	Tools   []Tool `json:"tools"`
}

type Server struct {
	ID            string `json:"id"`
	Title         string `json:"title,omitempty"`
	Source        string `json:"source,omitempty"`
	Compatibility string `json:"compatibility,omitempty"`
	Auth          string `json:"auth,omitempty"`
}

type Tool struct {
	Name          string            `json:"name"`
	Title         string            `json:"title,omitempty"`
	Description   string            `json:"description"`
	InputSchema   json.RawMessage   `json:"input_schema"`
	Compatibility string            `json:"compatibility,omitempty"`
	Auth          string            `json:"auth,omitempty"`
	Status        capability.Status `json:"status,omitempty"`
}

type Options struct {
	CatalogueID string
	Scope       capability.Scope
	TrustLevel  string
}

type Set struct {
	Documents []search.Document
	Details   []capability.Detail
}

// Provider retains the last successfully parsed authoritative snapshot in
// memory. A failed/unavailable refresh returns an error but never clears the
// previously accepted set.
type Provider struct {
	mu      sync.RWMutex
	current Set
	hasData bool
}

func (p *Provider) RefreshFile(path string, options Options) (Set, error) {
	set, err := LoadFile(path, options)
	if err != nil {
		return p.Current(), err
	}
	p.mu.Lock()
	p.current = cloneSet(set)
	p.hasData = true
	p.mu.Unlock()
	return cloneSet(set), nil
}

func (p *Provider) Current() Set {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.hasData {
		return Set{}
	}
	return cloneSet(p.current)
}

func LoadFile(path string, options Options) (Set, error) {
	file, err := os.Open(path)
	if err != nil {
		return Set{}, fmt.Errorf("open MCP tool catalogue %q: %w", path, err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, MaxSnapshotBytes+1))
	if err != nil {
		return Set{}, fmt.Errorf("read MCP tool catalogue %q: %w", path, err)
	}
	if len(raw) > MaxSnapshotBytes {
		return Set{}, fmt.Errorf("MCP tool catalogue %q exceeds %d bytes", path, MaxSnapshotBytes)
	}
	set, err := Parse(raw, options)
	if err != nil {
		return Set{}, fmt.Errorf("MCP tool catalogue %q: %w", path, err)
	}
	return set, nil
}

func Parse(raw []byte, options Options) (Set, error) {
	if len(raw) == 0 {
		return Set{}, fmt.Errorf("snapshot is empty")
	}
	if len(raw) > MaxSnapshotBytes {
		return Set{}, fmt.Errorf("snapshot exceeds %d bytes", MaxSnapshotBytes)
	}
	if err := validateIdentifier("catalogue id", options.CatalogueID, 128); err != nil {
		return Set{}, err
	}
	if err := options.Scope.Validate(); err != nil {
		return Set{}, fmt.Errorf("catalogue scope: %w", err)
	}
	trust := strings.TrimSpace(options.TrustLevel)
	if trust == "" {
		trust = "approved"
	}

	var snapshot Snapshot
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return Set{}, fmt.Errorf("decode snapshot: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Set{}, fmt.Errorf("snapshot contains trailing JSON values")
		}
		return Set{}, fmt.Errorf("decode trailing snapshot data: %w", err)
	}
	if snapshot.Version != SnapshotVersion {
		return Set{}, fmt.Errorf("unsupported snapshot version %d", snapshot.Version)
	}
	if err := validateIdentifier("server id", snapshot.Server.ID, 128); err != nil {
		return Set{}, err
	}
	if len(snapshot.Tools) == 0 {
		return Set{}, fmt.Errorf("snapshot requires at least one tool")
	}
	if len(snapshot.Tools) > MaxToolsPerSnapshot {
		return Set{}, fmt.Errorf("snapshot contains %d tools; maximum is %d", len(snapshot.Tools), MaxToolsPerSnapshot)
	}

	set := Set{
		Documents: make([]search.Document, 0, len(snapshot.Tools)),
		Details:   make([]capability.Detail, 0, len(snapshot.Tools)),
	}
	seen := make(map[string]struct{}, len(snapshot.Tools))
	for index, tool := range snapshot.Tools {
		if err := validateIdentifier("tool name", tool.Name, 256); err != nil {
			return Set{}, fmt.Errorf("tools[%d]: %w", index, err)
		}
		if strings.TrimSpace(tool.Description) == "" || len(tool.Description) > MaxDescriptionBytes {
			return Set{}, fmt.Errorf("tools[%d] description must be between 1 and %d bytes", index, MaxDescriptionBytes)
		}
		stableID := "mcp-tool:" + snapshot.Server.ID + ":" + tool.Name
		if _, exists := seen[stableID]; exists {
			return Set{}, fmt.Errorf("duplicate server/tool identity %q", stableID)
		}
		seen[stableID] = struct{}{}

		canonicalSchema, summary, schemaDigest, err := normalizeSchema(tool.InputSchema)
		if err != nil {
			return Set{}, fmt.Errorf("tools[%d] %q input schema: %w", index, tool.Name, err)
		}
		status := tool.Status
		if status == "" {
			status = capability.StatusActive
		}
		if status != capability.StatusActive && status != capability.StatusDeprecated && status != capability.StatusYanked {
			return Set{}, fmt.Errorf("tools[%d] %q has unsupported status %q", index, tool.Name, status)
		}
		compatibility := strings.TrimSpace(tool.Compatibility)
		if compatibility == "" {
			compatibility = strings.TrimSpace(snapshot.Server.Compatibility)
		}
		if compatibility == "" {
			compatibility = "mcp"
		}
		auth := strings.TrimSpace(tool.Auth)
		if auth == "" {
			auth = strings.TrimSpace(snapshot.Server.Auth)
		}
		revisionDigest := sha256.Sum256([]byte(strings.Join([]string{
			stableID,
			strings.TrimSpace(tool.Title),
			strings.TrimSpace(tool.Description),
			compatibility,
			auth,
			string(status),
			schemaDigest,
		}, "\x00")))
		revisionID := "mcp-tool-rev:" + hex.EncodeToString(revisionDigest[:])

		metadata := map[string]string{
			"server_id":            snapshot.Server.ID,
			"tool_name":            tool.Name,
			"input_schema_sha256":  schemaDigest,
			"input_schema_summary": summary,
			"metadata_trust":       "untrusted",
		}
		if tool.Title != "" {
			metadata["title"] = tool.Title
		}
		if auth != "" {
			metadata["auth"] = auth
		}
		descriptor := capability.Descriptor{
			Identity:      capability.Identity{ID: stableID, Kind: capability.KindTool},
			Name:          tool.Name,
			Description:   tool.Description,
			Compatibility: compatibility,
			Scope:         options.Scope,
			Source: capability.Source{
				RepositoryID: options.CatalogueID,
				URL:          snapshot.Server.Source,
			},
			Provenance: capability.Provenance{RevisionID: revisionID},
			TrustLevel: trust,
			Status:     status,
			Metadata:   metadata,
		}
		detail := capability.Detail{
			Descriptor: descriptor,
			Tool: &capability.ToolDetail{
				ServerID:           snapshot.Server.ID,
				ServerTitle:        snapshot.Server.Title,
				Name:               tool.Name,
				Title:              tool.Title,
				InputSchema:         append(json.RawMessage(nil), canonicalSchema...),
				InputSchemaSummary: summary,
				InputSchemaSHA256:  schemaDigest,
				Compatibility:      compatibility,
				Auth:               auth,
				Source:             snapshot.Server.Source,
				UntrustedMetadata:  true,
				ExecutionSupported: false,
			},
		}
		set.Documents = append(set.Documents, search.Document{
			ID:             revisionID,
			SkillID:        stableID,
			Name:           tool.Name,
			Description:    tool.Description,
			Compatibility:  compatibility,
			OrganizationID: options.Scope.Organization,
			RepositoryID:   options.CatalogueID,
			TrustLevel:     trust,
			Searchable:     status != capability.StatusYanked,
		})
		set.Details = append(set.Details, detail)
	}
	return set, nil
}

func normalizeSchema(raw json.RawMessage) (json.RawMessage, string, string, error) {
	if len(raw) == 0 {
		return nil, "", "", fmt.Errorf("schema is required")
	}
	if len(raw) > MaxSchemaBytes {
		return nil, "", "", fmt.Errorf("schema exceeds %d bytes", MaxSchemaBytes)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, "", "", fmt.Errorf("invalid JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, "", "", fmt.Errorf("schema must contain exactly one JSON value")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, "", "", fmt.Errorf("schema root must be an object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, "", "", fmt.Errorf("canonicalize schema: %w", err)
	}
	if len(canonical) > MaxSchemaBytes {
		return nil, "", "", fmt.Errorf("normalized schema exceeds %d bytes", MaxSchemaBytes)
	}
	digest := sha256.Sum256(canonical)
	digestText := "sha256:" + hex.EncodeToString(digest[:])
	return canonical, schemaSummary(object), digestText, nil
}

func schemaSummary(schema map[string]any) string {
	parts := make([]string, 0, 4)
	if value, ok := schema["type"].(string); ok && value != "" {
		parts = append(parts, "type="+value)
	}
	if properties, ok := schema["properties"].(map[string]any); ok && len(properties) > 0 {
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) > 12 {
			names = append(names[:12], fmt.Sprintf("+%d-more", len(properties)-12))
		}
		parts = append(parts, "properties="+strings.Join(names, ","))
	}
	if required, ok := schema["required"].([]any); ok && len(required) > 0 {
		names := make([]string, 0, len(required))
		for _, item := range required {
			if name, ok := item.(string); ok {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if len(names) > 12 {
			names = append(names[:12], fmt.Sprintf("+%d-more", len(required)-12))
		}
		parts = append(parts, "required="+strings.Join(names, ","))
	}
	if additional, ok := schema["additionalProperties"].(bool); ok {
		parts = append(parts, fmt.Sprintf("additional_properties=%t", additional))
	}
	if len(parts) == 0 {
		parts = append(parts, "object-schema")
	}
	summary := strings.Join(parts, "; ")
	if len(summary) > maxSchemaSummaryBytes {
		summary = summary[:maxSchemaSummaryBytes]
	}
	return summary
}

func validateIdentifier(name, value string, max int) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > max {
		return fmt.Errorf("%s must be between 1 and %d bytes without surrounding whitespace", name, max)
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) || !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.:/", r)) {
			return fmt.Errorf("invalid %s %q", name, value)
		}
	}
	return nil
}

func cloneSet(input Set) Set {
	out := Set{
		Documents: append([]search.Document(nil), input.Documents...),
		Details:   append([]capability.Detail(nil), input.Details...),
	}
	for index := range out.Documents {
		if metadata := out.Documents[index].Metadata; metadata != nil {
			copyMetadata := make(map[string]string, len(metadata))
			for key, value := range metadata {
				copyMetadata[key] = value
			}
			out.Documents[index].Metadata = copyMetadata
		}
	}
	for index := range out.Details {
		if metadata := out.Details[index].Descriptor.Metadata; metadata != nil {
			copyMetadata := make(map[string]string, len(metadata))
			for key, value := range metadata {
				copyMetadata[key] = value
			}
			out.Details[index].Descriptor.Metadata = copyMetadata
		}
		if tool := out.Details[index].Tool; tool != nil {
			copyTool := *tool
			copyTool.InputSchema = append(json.RawMessage(nil), tool.InputSchema...)
			out.Details[index].Tool = &copyTool
		}
	}
	return out
}
