package capability

import (
	"testing"

	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/search"
)

func TestAllowsNewSelectionByLifecycleAndApproval(t *testing.T) {
	active := doc("rev-active", "demo/central/active", "central", "active", "active")
	deprecated := doc("rev-deprecated", "demo/central/deprecated", "central", "deprecated", "deprecated")
	deprecated.Metadata = map[string]string{
		governance.DeprecatedKey: "true",
		governance.ReplacedByKey: "demo/central/active",
	}
	yanked := doc("rev-yanked", "demo/central/yanked", "central", "yanked", "yanked")
	yanked.Metadata = map[string]string{governance.StateKey: string(StatusYanked)}
	unapproved := doc("rev-internal", "demo/central/internal", "central", "internal", "internal")
	unapproved.TrustLevel = "internal"

	service, err := New(newTestIndex(t, []search.Document{active, deprecated, yanked, unapproved}), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, revisionID := range []string{"rev-active", "rev-deprecated"} {
		if err := service.AllowsNewSelection(revisionID); err != nil {
			t.Fatalf("%s should allow new selection: %v", revisionID, err)
		}
	}
	for _, revisionID := range []string{"rev-yanked", "rev-internal", "missing"} {
		if err := service.AllowsNewSelection(revisionID); err == nil {
			t.Fatalf("%s unexpectedly allowed new selection", revisionID)
		}
	}
}
