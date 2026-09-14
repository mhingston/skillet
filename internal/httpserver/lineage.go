package httpserver

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/lineage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const improvementLineageEnv = "SKILLET_IMPROVEMENT_LINEAGE"

var lineageStores sync.Map // map[*Server]*lineage.Store

type recordRevisionLineageInput struct {
	ParentRevisionID string   `json:"parent_revision_id" jsonschema:"Exact immutable parent/base revision identifier"`
	DescendantKind   string   `json:"descendant_kind" jsonschema:"Descendant identity kind: candidate or revision"`
	DescendantID     string   `json:"descendant_id" jsonschema:"Candidate or immutable revision identifier"`
	Relationship     string   `json:"relationship" jsonschema:"Lineage relationship: derived_from, challenger_of, or superseded_by"`
	ExperimentIDs    []string `json:"experiment_ids" jsonschema:"One or more exact M4 experiment identifiers that produced/evaluated the descendant"`
	CorrelationID    string   `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type recordLineageDecisionInput struct {
	LineageID     string `json:"lineage_id" jsonschema:"Immutable lineage assertion identifier"`
	State         string `json:"state" jsonschema:"Terminal decision: promoted or rejected"`
	Reference     string `json:"reference" jsonschema:"Bounded reference to the promotion/rejection decision evidence"`
	CorrelationID string `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type revisionLineageInput struct {
	RevisionID string `json:"revision_id" jsonschema:"Exact immutable revision identifier to view"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum parent and descendant assertions to return per side (1-100; default 25)"`
}

func addLineageTools(server *mcp.Server, app *Server) {
	if server == nil || app == nil || app.catalogue == nil || !improvementLineageEnabled() {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "record_revision_lineage",
		Description: "Append an immutable experiment-backed lineage assertion between an exact base revision and a candidate/revision descendant. This never activates, ranks, trusts, resolves, or mutates source.",
	}, app.recordRevisionLineageTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "record_lineage_decision",
		Description: "Append the immutable promoted/rejected decision reference for one lineage assertion. The decision is historical evidence only and does not change canonical capability state.",
	}, app.recordLineageDecisionTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_revision_lineage",
		Description: "Return a bounded authorised lineage view for one immutable revision, including direct parents/descendants, producing experiment/eval evidence, and terminal decision state.",
	}, app.getRevisionLineageTool)
}

func improvementLineageEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(improvementLineageEnv)), "true")
}

func (s *Server) recordRevisionLineageTool(ctx context.Context, _ *mcp.CallToolRequest, input recordRevisionLineageInput) (*mcp.CallToolResult, lineage.Entry, error) {
	organizationID, actorID := s.proposalActor(ctx)
	input.ParentRevisionID = strings.TrimSpace(input.ParentRevisionID)
	if input.ParentRevisionID == "" {
		return nil, lineage.Entry{}, fmt.Errorf("parent_revision_id is required")
	}
	info, err := s.catalogue.Revision(ctx, organizationID, input.ParentRevisionID)
	if err != nil {
		return nil, lineage.Entry{}, err
	}
	resource := s.capabilityAuthorizationResource(organizationID, info)
	if err := s.authorize(ctx, authz.ActionLineageWrite, resource); err != nil {
		return nil, lineage.Entry{}, err
	}
	store, err := s.lineageStore(ctx)
	if err != nil {
		return nil, lineage.Entry{}, err
	}
	entry, err := store.Record(ctx, lineage.RecordInput{
		OrganizationID:  organizationID,
		ActorID:         actorID,
		CorrelationID:   strings.TrimSpace(input.CorrelationID),
		ParentRevisionID: input.ParentRevisionID,
		DescendantKind:  strings.TrimSpace(input.DescendantKind),
		DescendantID:    strings.TrimSpace(input.DescendantID),
		Relationship:    strings.TrimSpace(input.Relationship),
		ExperimentIDs:   input.ExperimentIDs,
	})
	if err != nil {
		return nil, lineage.Entry{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "improvement_lineage_recorded", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "skill_id": entry.Record.CapabilityID,
		"revision_id": entry.Record.ParentRevisionID, "lineage_id": entry.Record.ID,
		"descendant_kind": entry.Record.DescendantKind, "descendant_id": entry.Record.DescendantID,
		"relationship": entry.Record.Relationship, "request_id": entry.Record.CorrelationID,
	}); err != nil {
		return nil, lineage.Entry{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Recorded immutable lineage evidence. Capability activation, version resolution, governance, dependencies, and retrieval ranking were unchanged."}}}, entry, nil
}

func (s *Server) recordLineageDecisionTool(ctx context.Context, _ *mcp.CallToolRequest, input recordLineageDecisionInput) (*mcp.CallToolResult, lineage.Entry, error) {
	organizationID, actorID := s.proposalActor(ctx)
	store, err := s.lineageStore(ctx)
	if err != nil {
		return nil, lineage.Entry{}, err
	}
	lineageID := strings.TrimSpace(input.LineageID)
	entry, err := store.Get(ctx, organizationID, lineageID)
	if err != nil {
		return nil, lineage.Entry{}, err
	}
	info, err := s.catalogue.Revision(ctx, organizationID, entry.Record.ParentRevisionID)
	if err != nil {
		return nil, lineage.Entry{}, err
	}
	resource := s.capabilityAuthorizationResource(organizationID, info)
	if err := s.authorize(ctx, authz.ActionLineageDecide, resource); err != nil {
		return nil, lineage.Entry{}, err
	}
	entry, err = store.Decide(ctx, lineage.DecisionInput{
		OrganizationID: organizationID,
		ActorID:        actorID,
		CorrelationID:  strings.TrimSpace(input.CorrelationID),
		LineageID:      lineageID,
		State:          strings.TrimSpace(input.State),
		Reference:      strings.TrimSpace(input.Reference),
	})
	if err != nil {
		return nil, lineage.Entry{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "improvement_lineage_decision_recorded", map[string]any{
		"actor_type": "agent_or_human", "actor_id": actorID, "skill_id": entry.Record.CapabilityID,
		"revision_id": entry.Record.ParentRevisionID, "lineage_id": entry.Record.ID,
		"decision": entry.Decision.State, "decision_reference": entry.Decision.Reference,
		"request_id": strings.TrimSpace(input.CorrelationID),
	}); err != nil {
		return nil, lineage.Entry{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Recorded immutable lineage decision evidence. Promotion/rejection state does not itself activate or rank a revision."}}}, entry, nil
}

func (s *Server) getRevisionLineageTool(ctx context.Context, _ *mcp.CallToolRequest, input revisionLineageInput) (*mcp.CallToolResult, lineage.View, error) {
	organizationID, _ := s.proposalActor(ctx)
	revisionID := strings.TrimSpace(input.RevisionID)
	if revisionID == "" {
		return nil, lineage.View{}, fmt.Errorf("revision_id is required")
	}
	info, err := s.catalogue.Revision(ctx, organizationID, revisionID)
	if err != nil {
		return nil, lineage.View{}, err
	}
	resource := s.capabilityAuthorizationResource(organizationID, info)
	if err := s.authorize(ctx, authz.ActionLineageRead, resource); err != nil {
		return nil, lineage.View{}, err
	}
	store, err := s.lineageStore(ctx)
	if err != nil {
		return nil, lineage.View{}, err
	}
	view, err := store.View(ctx, organizationID, revisionID, input.Limit)
	if err != nil {
		return nil, lineage.View{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Returned %d parent and %d descendant lineage assertion(s) for the exact revision.", len(view.Parents), len(view.Descendants))}}}, view, nil
}

func (s *Server) lineageStore(ctx context.Context) (*lineage.Store, error) {
	if existing, ok := lineageStores.Load(s); ok {
		store, _ := existing.(*lineage.Store)
		if store != nil {
			return store, nil
		}
	}
	if s == nil || s.catalogue == nil {
		return nil, fmt.Errorf("lineage persistence is unavailable")
	}
	store, err := lineage.New(ctx, s.catalogue)
	if err != nil {
		return nil, err
	}
	actual, _ := lineageStores.LoadOrStore(s, store)
	resolved, _ := actual.(*lineage.Store)
	if resolved == nil {
		return nil, fmt.Errorf("lineage persistence is unavailable")
	}
	return resolved, nil
}
