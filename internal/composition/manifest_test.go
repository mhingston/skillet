package composition

import "testing"

func TestParseManifestDeterministic(t *testing.T) {
	manifest, err := ParseManifest([]byte(`version: 1
collections:
  - id: review-kit
    name: Review kit
    description: Source-controlled review capabilities
    members:
      - id: demo/repo/zeta
        version: "^2.0.0"
      - id: demo/repo/alpha
`))
	if err != nil { t.Fatal(err) }
	if len(manifest.Collections) != 1 { t.Fatalf("manifest=%+v", manifest) }
	members := manifest.Collections[0].Members
	if len(members) != 2 || members[0].ID != "demo/repo/alpha" || members[1].ID != "demo/repo/zeta" {
		t.Fatalf("members=%+v", members)
	}
}

func TestParseManifestRejectsWorkflowLikeUnknownFields(t *testing.T) {
	_, err := ParseManifest([]byte(`version: 1
collections:
  - id: review-kit
    name: Review kit
    sequence:
      - demo/repo/first
    members:
      - id: demo/repo/first
`))
	if err == nil { t.Fatal("expected unknown workflow field to be rejected") }
}
