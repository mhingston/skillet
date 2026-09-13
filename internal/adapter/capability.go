package adapter

import (
	"context"
	"fmt"

	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type CapabilityScope struct {
	Namespace  string `json:"namespace,omitempty"`
	Repository string `json:"repository,omitempty"`
}

type CapabilitySearchResult struct {
	QueryID    string          `json:"query_id"`
	Degraded   map[string]bool `json:"degraded"`
	Candidates []struct {
		CandidateID string                `json:"candidate_id"`
		Capability  capability.Descriptor `json:"capability"`
		Ranking     search.Hit            `json:"ranking"`
	} `json:"candidates"`
}

type CapabilityDescribeResult struct {
	Detail                 capability.Detail `json:"detail"`
	MaterializeCandidateID string            `json:"materialize_candidate_id,omitempty"`
}

func (c Client) SearchCapabilities(ctx context.Context, query string, scope CapabilityScope, limit int) (CapabilitySearchResult, error) {
	s, err := c.connect(ctx)
	if err != nil {
		return CapabilitySearchResult{}, err
	}
	defer s.Close()
	r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "search_capabilities", Arguments: map[string]any{
		"query": query,
		"scope": map[string]any{"namespace": scope.Namespace, "repository": scope.Repository},
		"limit": limit,
	}})
	if err != nil {
		return CapabilitySearchResult{}, err
	}
	if r.IsError {
		return CapabilitySearchResult{}, fmt.Errorf("search_capabilities returned an error")
	}
	var out CapabilitySearchResult
	if err := decodeStructured(r.StructuredContent, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c Client) DescribeCapability(ctx context.Context, candidateID string, scope CapabilityScope) (CapabilityDescribeResult, error) {
	s, err := c.connect(ctx)
	if err != nil {
		return CapabilityDescribeResult{}, err
	}
	defer s.Close()
	r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "describe_capability", Arguments: map[string]any{
		"candidate_id": candidateID,
		"scope": map[string]any{"namespace": scope.Namespace, "repository": scope.Repository},
	}})
	if err != nil {
		return CapabilityDescribeResult{}, err
	}
	if r.IsError {
		return CapabilityDescribeResult{}, fmt.Errorf("describe_capability returned an error")
	}
	var out CapabilityDescribeResult
	if err := decodeStructured(r.StructuredContent, &out); err != nil {
		return out, err
	}
	return out, nil
}
