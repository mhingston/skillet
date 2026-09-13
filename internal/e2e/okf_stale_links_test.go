package e2e

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/httpserver"
	"github.com/mhingston/skillet/internal/knowledge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOKFReconciliationRemovesStaleExplicitLinksThroughMCP(t *testing.T) {
	ctx := context.Background()
	bundleRoot := filepath.Join(t.TempDir(), "bundle")
	copyFixtureTree(t, filepath.Join("..", "knowledge", "testdata", "okf-v02"), bundleRoot)
	service, err := knowledge.Open(ctx, filepath.Join(t.TempDir(), "data"), knowledge.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: "retention", Root: bundleRoot, Revision: "rev-1"}}); err != nil {
		t.Fatal(err)
	}

	app := httpserver.New(nil, nil)
	app.ConfigureKnowledge(service)
	server := httptest.NewServer(app.Handler("/mcp", 1<<20, httpserver.AuthConfig{Mode: "development", OrganizationID: "demo"}))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "okf-links-e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	glossary := callKnowledgeSearch(t, ctx, session, "NBA next best action", 5)
	glossaryDocumentID := findDocumentByTitle(t, glossary, "Retention glossary")
	before := callBacklinks(t, ctx, session, glossaryDocumentID)
	if len(before) != 1 || before[0].Path != "overview.md" {
		t.Fatalf("initial glossary backlinks = %+v, want overview.md", before)
	}

	if err := os.WriteFile(filepath.Join(bundleRoot, "overview.md"), []byte(`---
type: Playbook
title: Retention coaching overview
description: Updated entry point with the glossary link removed.
generated: {by: process:fixture-generator, at: 2026-09-12T12:00:00Z}
status: stable
---

# Purpose

Updated guidance links only to the [renewals policy](/policies/renewals.md).
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: "retention", Root: bundleRoot, Revision: "rev-2"}}); err != nil {
		t.Fatal(err)
	}

	after := callBacklinks(t, ctx, session, glossaryDocumentID)
	if len(after) != 0 {
		t.Fatalf("stale glossary backlink survived reconciliation: %+v", after)
	}
}
