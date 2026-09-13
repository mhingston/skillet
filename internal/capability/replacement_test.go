package capability

import (
	"testing"

	"github.com/mhingston/skillet/internal/search"
)

func TestDescribeNeverSubstitutesDeprecatedReplacementMetadata(t *testing.T) {
	old := doc("rev-old", "demo/central/old", "central", "old-release", "Legacy release procedure")
	old.Metadata = map[string]string{
		"status":      "deprecated",
		"replacement": "demo/central/new",
	}
	newer := doc("rev-new", "demo/central/new", "central", "new-release", "Replacement release procedure")

	idx := newTestIndex(t, []search.Document{old, newer})
	service, err := New(idx, nil)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := service.Describe("rev-old", mustScope(t, "demo", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if selected.Identity.ID != "demo/central/old" || selected.Provenance.RevisionID != "rev-old" {
		t.Fatalf("selected capability was substituted: %+v", selected)
	}
	if selected.Metadata["status"] != "deprecated" || selected.Metadata["replacement"] != "demo/central/new" {
		t.Fatalf("replacement metadata was not preserved as inert metadata: %+v", selected.Metadata)
	}

	old.Metadata["replacement"] = "demo/central/missing"
	idx = newTestIndex(t, []search.Document{old, newer})
	service, err = New(idx, nil)
	if err != nil {
		t.Fatal(err)
	}
	selected, err = service.Describe("rev-old", mustScope(t, "demo", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if selected.Identity.ID != "demo/central/old" || selected.Provenance.RevisionID != "rev-old" {
		t.Fatalf("unresolved replacement metadata changed selection: %+v", selected)
	}
}
