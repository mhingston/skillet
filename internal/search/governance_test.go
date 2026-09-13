package search

import (
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/governance"
)

func TestRoutingTextExcludesGovernanceControlMetadata(t *testing.T) {
	doc := Document{
		Name:          "release",
		Description:   "plan a production release",
		Compatibility: "go",
		Metadata: map[string]string{
			"intent":                  "deploy rollback",
			governance.StateKey:        "deprecated",
			governance.OwnerKey:        "secret-owner-token",
			governance.MaintainersKey:  "secret-maintainer-token",
			governance.ReasonKey:       "secret-reason-token",
			governance.DeprecatedKey:   "true",
			governance.ReplacedByKey:   "secret-replacement-token",
		},
	}
	text := routingText(doc)
	if !strings.Contains(text, "intent: deploy rollback") {
		t.Fatalf("semantic metadata missing from routing text: %q", text)
	}
	for _, forbidden := range []string{"deprecated", "secret-owner-token", "secret-maintainer-token", "secret-reason-token", "secret-replacement-token"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("governance value %q leaked into routing text: %q", forbidden, text)
		}
	}
}
