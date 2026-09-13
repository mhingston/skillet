package rerank

import (
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/governance"
)

func TestRerankerPromptExcludesGovernanceControlMetadata(t *testing.T) {
	prompt, err := userPrompt("release procedure", []Candidate{{
		ID:          "release",
		Name:        "release",
		Description: "Release the service safely.",
		Metadata: map[string]string{
			"intent":                  "release",
			governance.StateKey:       "deprecated",
			governance.OwnerKey:       "ranking-poison-owner",
			governance.MaintainersKey: "ranking-poison-maintainer",
			governance.ReasonKey:      "ranking-poison-reason",
			governance.ReplacedByKey:  "demo/central/ranking-poison-successor",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		governance.StateKey,
		governance.OwnerKey,
		governance.MaintainersKey,
		governance.ReasonKey,
		governance.ReplacedByKey,
		"ranking-poison-owner",
		"ranking-poison-maintainer",
		"ranking-poison-reason",
		"ranking-poison-successor",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("governance control metadata reached reranker prompt: %q", forbidden)
		}
	}
	if !strings.Contains(prompt, `"intent":"release"`) {
		t.Fatalf("semantic metadata was removed from reranker prompt: %s", prompt)
	}
}
