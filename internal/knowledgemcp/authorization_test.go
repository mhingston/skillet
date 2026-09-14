package knowledgemcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/knowledge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestKnowledgeAuthorizationFiltersAfterRankingAndDeniesRead(t *testing.T) {
	ctx := context.Background()
	bundle := t.TempDir()
	writeConcept(t, bundle, "first.md", `---
type: Reference
title: First
---

# First
commonterm alpha
`)
	writeConcept(t, bundle, "second.md", `---
type: Reference
title: Second
---

# Second
commonterm beta
`)
	service, err := knowledge.Open(ctx, t.TempDir(), knowledge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: "authorization", Root: bundle, Revision: "r1"}}); err != nil {
		t.Fatal(err)
	}
	baseline, err := service.SearchOKF(ctx, "commonterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Results) < 2 {
		t.Fatalf("baseline result count = %d, want >= 2", len(baseline.Results))
	}
	allowed := baseline.Results[len(baseline.Results)-1]
	denied := baseline.Results[0]
	if allowed.Result.DocumentID == denied.Result.DocumentID || allowed.Result.ChunkID == denied.Result.ChunkID {
		t.Fatal("fixture did not produce distinct knowledge resources")
	}

	errDenied := errors.New("not entitled")
	authorize := func(_ context.Context, action authz.Action, resource authz.Resource) error {
		switch action {
		case authz.ActionKnowledgeSearch:
			if resource.ID == "" || resource.ID == allowed.Result.DocumentID {
				return nil
			}
		case authz.ActionKnowledgeRead:
			if resource.ID == allowed.Result.DocumentID {
				return nil
			}
		}
		return errDenied
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "knowledge-authz-test", Version: "1"}, nil)
	AddTools(mcpServer, service, authorize)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "knowledge-authz-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result := callTool(t, ctx, session, "search_knowledge", map[string]any{"query": "commonterm", "limit": 10})
	var filtered knowledge.OKFSearchResponse
	decodeStructured(t, result.StructuredContent, &filtered)
	if len(filtered.Results) != 1 || filtered.Results[0].Result.DocumentID != allowed.Result.DocumentID {
		t.Fatalf("filtered results = %+v", filtered.Results)
	}
	got := filtered.Results[0].Result
	want := allowed.Result
	if got.Rank != want.Rank || got.FusedScore != want.FusedScore || got.LexicalRank != want.LexicalRank || got.VectorRank != want.VectorRank {
		t.Fatalf("authorization changed ranking evidence: got=%+v want=%+v", got, want)
	}

	deniedRead := callToolAllowError(t, ctx, session, "read_knowledge", map[string]any{"chunk_id": denied.Result.ChunkID})
	if !deniedRead.IsError {
		t.Fatal("unauthorized knowledge read unexpectedly succeeded")
	}
	allowedRead := callToolAllowError(t, ctx, session, "read_knowledge", map[string]any{"chunk_id": allowed.Result.ChunkID})
	if allowedRead.IsError {
		t.Fatalf("authorized knowledge read failed: %+v", allowedRead.Content)
	}
}
