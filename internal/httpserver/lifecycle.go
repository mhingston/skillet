package httpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type lifecycleReference struct {
	RevisionID        string `json:"revision_id"`
	SkillID           string `json:"skill_id"`
	Commit            string `json:"commit"`
	Tree              string `json:"tree"`
	ArchiveSHA256     string `json:"archive_sha256"`
	QueryID           string `json:"query_id,omitempty"`
	MaterializationID string `json:"materialization_id,omitempty"`
}

type lifecycleInput struct {
	Lifecycle     lifecycleReference `json:"lifecycle"`
	Event         string             `json:"event" jsonschema:"Host-observed event: activated, deactivated, completed, or failed"`
	CorrelationID string             `json:"correlation_id,omitempty" jsonschema:"Optional opaque session or run correlation identifier"`
	Source        string             `json:"source,omitempty" jsonschema:"Optional harness or adapter identifier"`
}

type lifecycleOutput struct {
	Status     string `json:"status"`
	Event      string `json:"event"`
	RevisionID string `json:"revision_id"`
}

func (s *Server) lifecycleTool(ctx context.Context, _ *mcp.CallToolRequest, input lifecycleInput) (*mcp.CallToolResult, lifecycleOutput, error) {
	if s.catalogue == nil {
		return nil, lifecycleOutput{}, fmt.Errorf("lifecycle telemetry is unavailable")
	}
	input.Event = strings.TrimSpace(input.Event)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.Source = strings.TrimSpace(input.Source)
	if len(input.CorrelationID) > 128 {
		return nil, lifecycleOutput{}, fmt.Errorf("correlation_id exceeds 128 characters")
	}
	if len(input.Source) > 64 {
		return nil, lifecycleOutput{}, fmt.Errorf("source exceeds 64 characters")
	}
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	observation := catalogue.LifecycleObservation{
		RevisionID:        input.Lifecycle.RevisionID,
		SkillID:           input.Lifecycle.SkillID,
		Commit:            input.Lifecycle.Commit,
		Tree:              input.Lifecycle.Tree,
		ArchiveSHA256:     input.Lifecycle.ArchiveSHA256,
		QueryID:           input.Lifecycle.QueryID,
		MaterializationID: input.Lifecycle.MaterializationID,
		Event:             input.Event,
		CorrelationID:     input.CorrelationID,
		Source:            input.Source,
	}
	if err := s.catalogue.RecordLifecycle(ctx, organizationID, observation); err != nil {
		return nil, lifecycleOutput{}, err
	}
	out := lifecycleOutput{Status: "recorded", Event: input.Event, RevisionID: input.Lifecycle.RevisionID}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Lifecycle observation recorded as optional evidence."}}}, out, nil
}
