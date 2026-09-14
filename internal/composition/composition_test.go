package composition

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/mhingston/skillet/internal/capability"
)

func TestParseMetadata(t *testing.T) {
	metadata := map[string]string{
		MetadataRequires:   `[{"id":"demo/repo/base","version":"^1.2.0","kind":"skill"}]`,
		MetadataRecommends: `[{"id":"demo/repo/optional"}]`,
		MetadataConflicts:  `[{"id":"demo/repo/legacy","version":"<2.0.0"}]`,
	}
	relations, err := ParseMetadata(metadata)
	if err != nil { t.Fatal(err) }
	if len(relations.Requires) != 1 || relations.Requires[0].Version != "^1.2.0" { t.Fatalf("requires=%+v", relations.Requires) }
	if len(relations.Recommends) != 1 || len(relations.Conflicts) != 1 { t.Fatalf("relations=%+v", relations) }
}

func TestResolveSingleAndTransitiveDependencies(t *testing.T) {
	snapshot := Snapshot{Nodes: []Node{
		node("app", "1.0.0", "r-app", Relations{Requires: []Reference{{ID: "middle", Version: "^1.0.0"}}}),
		node("middle", "1.1.0", "r-middle", Relations{Requires: []Reference{{ID: "base", Version: ">=2.0.0 <3.0.0"}}}),
		node("base", "2.4.0", "r-base", Relations{}),
	}}
	plan, err := Resolve(snapshot, Selection{Capability: &Reference{ID: "app", RevisionID: "r-app"}})
	if err != nil { t.Fatal(err) }
	if got, want := capabilityIDs(plan), []string{"base", "middle", "app"}; strings.Join(got, ",") != strings.Join(want, ",") { t.Fatalf("order=%v want=%v", got, want) }
	if len(plan.Edges) != 3 { t.Fatalf("edges=%+v", plan.Edges) }
}

func TestResolveCompatibleRangesBacktracksToHighestCommonVersion(t *testing.T) {
	snapshot := Snapshot{Nodes: []Node{
		node("root", "1.0.0", "root", Relations{Requires: []Reference{{ID: "a"}, {ID: "shared", Version: ">=1.0.0 <3.0.0"}}}),
		node("a", "1.0.0", "a", Relations{Requires: []Reference{{ID: "shared", Version: ">=1.5.0 <2.0.0"}}}),
		node("shared", "2.5.0", "shared-2", Relations{}),
		node("shared", "1.8.0", "shared-1", Relations{}),
	}}
	plan, err := Resolve(snapshot, Selection{Capability: &Reference{ID: "root", RevisionID: "root"}})
	if err != nil { t.Fatal(err) }
	for _, locked := range plan.Capabilities {
		if locked.ID == "shared" && locked.Version != "1.8.0" { t.Fatalf("shared=%+v", locked) }
	}
}

func TestResolveUnsatisfiableRanges(t *testing.T) {
	snapshot := Snapshot{Nodes: []Node{
		node("root", "1.0.0", "root", Relations{Requires: []Reference{{ID: "a"}, {ID: "shared", Version: ">=2.0.0"}}}),
		node("a", "1.0.0", "a", Relations{Requires: []Reference{{ID: "shared", Version: "<2.0.0"}}}),
		node("shared", "1.9.0", "shared-1", Relations{}),
		node("shared", "2.1.0", "shared-2", Relations{}),
	}}
	_, err := Resolve(snapshot, Selection{Capability: &Reference{ID: "root", RevisionID: "root"}})
	if !IsCode(err, ErrorUnsatisfiable) { t.Fatalf("err=%v", err) }
}

func TestResolveCycle(t *testing.T) {
	snapshot := Snapshot{Nodes: []Node{
		node("a", "1.0.0", "a", Relations{Requires: []Reference{{ID: "b"}}}),
		node("b", "1.0.0", "b", Relations{Requires: []Reference{{ID: "a"}}}),
	}}
	_, err := Resolve(snapshot, Selection{Capability: &Reference{ID: "a", RevisionID: "a"}})
	if !IsCode(err, ErrorCycle) { t.Fatalf("err=%v", err) }
}

func TestResolveExplicitConflict(t *testing.T) {
	snapshot := Snapshot{Nodes: []Node{
		node("root", "1.0.0", "root", Relations{Requires: []Reference{{ID: "a"}, {ID: "b"}}}),
		node("a", "1.0.0", "a", Relations{Conflicts: []Reference{{ID: "b"}}}),
		node("b", "1.0.0", "b", Relations{}),
	}}
	_, err := Resolve(snapshot, Selection{Capability: &Reference{ID: "root", RevisionID: "root"}})
	if !IsCode(err, ErrorConflict) { t.Fatalf("err=%v", err) }
}

func TestResolveYankedOrInaccessibleFailsClosed(t *testing.T) {
	root := node("root", "1.0.0", "root", Relations{Requires: []Reference{{ID: "hidden"}}})
	hidden := node("hidden", "1.0.0", "hidden", Relations{})
	hidden.Selectable = false
	_, err := Resolve(Snapshot{Nodes: []Node{root, hidden}}, Selection{Capability: &Reference{ID: "root", RevisionID: "root"}})
	if !IsCode(err, ErrorUnsatisfiable) { t.Fatalf("yanked err=%v", err) }
	_, err = Resolve(Snapshot{Nodes: []Node{root}}, Selection{Capability: &Reference{ID: "root", RevisionID: "root"}})
	if !IsCode(err, ErrorUnavailable) || strings.Contains(err.Error(), "hidden") { t.Fatalf("inaccessible err=%v", err) }
}

func TestResolveDeprecatedDoesNotSubstituteReplacement(t *testing.T) {
	root := node("root", "1.0.0", "root", Relations{Requires: []Reference{{ID: "old"}}})
	old := node("old", "1.0.0", "old", Relations{})
	old.Detail.Descriptor.Status = capability.StatusDeprecated
	old.Detail.Descriptor.Governance.ReplacedBy = "new"
	newNode := node("new", "2.0.0", "new", Relations{})
	plan, err := Resolve(Snapshot{Nodes: []Node{root, old, newNode}}, Selection{Capability: &Reference{ID: "root", RevisionID: "root"}})
	if err != nil { t.Fatal(err) }
	ids := capabilityIDs(plan)
	if strings.Contains(strings.Join(ids, ","), "new") || !strings.Contains(strings.Join(ids, ","), "old") { t.Fatalf("ids=%v", ids) }
}

func TestResolveDeduplicatesDependencyReachedByMultiplePaths(t *testing.T) {
	snapshot := Snapshot{Nodes: []Node{
		node("root", "1.0.0", "root", Relations{Requires: []Reference{{ID: "a"}, {ID: "b"}}}),
		node("a", "1.0.0", "a", Relations{Requires: []Reference{{ID: "shared"}}}),
		node("b", "1.0.0", "b", Relations{Requires: []Reference{{ID: "shared"}}}),
		node("shared", "1.0.0", "shared", Relations{}),
	}}
	plan, err := Resolve(snapshot, Selection{Capability: &Reference{ID: "root", RevisionID: "root"}})
	if err != nil { t.Fatal(err) }
	count := 0
	for _, item := range plan.Capabilities { if item.ID == "shared" { count++ } }
	if count != 1 { t.Fatalf("shared count=%d plan=%+v", count, plan.Capabilities) }
	paths := 0
	for _, edge := range plan.Edges { if edge.To == "shared" { paths++ } }
	if paths != 2 { t.Fatalf("shared edges=%d edges=%+v", paths, plan.Edges) }
}

func TestResolveCollectionExpansion(t *testing.T) {
	snapshot := Snapshot{
		Nodes: []Node{node("a", "1.0.0", "a", Relations{}), node("b", "2.0.0", "b", Relations{})},
		Collections: []Collection{{ID: "review-kit", Name: "Review kit", Members: []Reference{{ID: "b", Version: "2.0.0"}, {ID: "a"}}}},
	}
	plan, err := Resolve(snapshot, Selection{Collection: "review-kit"})
	if err != nil { t.Fatal(err) }
	if got := capabilityIDs(plan); strings.Join(got, ",") != "a,b" { t.Fatalf("ids=%v", got) }
}

func TestResolutionLocksImmutableDigestsAndIsDeterministic(t *testing.T) {
	base := []Node{
		node("root", "1.0.0", "root-r1", Relations{Requires: []Reference{{ID: "dep", Version: "^1.0.0"}}}),
		node("dep", "1.2.0", "dep-r2", Relations{}),
		node("dep", "1.1.0", "dep-r1", Relations{}),
	}
	first, err := Resolve(Snapshot{Nodes: append([]Node(nil), base...)}, Selection{Capability: &Reference{ID: "root", RevisionID: "root-r1"}})
	if err != nil { t.Fatal(err) }
	encodedFirst, _ := json.Marshal(first)
	for i := 0; i < 20; i++ {
		shuffled := append([]Node(nil), base...)
		rand.New(rand.NewSource(int64(i + 1))).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		plan, resolveErr := Resolve(Snapshot{Nodes: shuffled}, Selection{Capability: &Reference{ID: "root", RevisionID: "root-r1"}})
		if resolveErr != nil { t.Fatal(resolveErr) }
		encoded, _ := json.Marshal(plan)
		if string(encoded) != string(encodedFirst) { t.Fatalf("nondeterministic\nfirst=%s\nnext=%s", encodedFirst, encoded) }
	}
	for _, locked := range first.Capabilities {
		if locked.ID == "dep" && (locked.RevisionID != "dep-r2" || locked.ArchiveSHA256TarGZ == "") { t.Fatalf("dep lock=%+v", locked) }
	}
}

func TestExactLockedRevisionDoesNotFollowNewerRevision(t *testing.T) {
	snapshot := Snapshot{Nodes: []Node{
		node("skill", "1.0.0", "r1", Relations{}),
		node("skill", "2.0.0", "r2", Relations{}),
	}}
	plan, err := Resolve(snapshot, Selection{Capability: &Reference{ID: "skill", RevisionID: "r1"}})
	if err != nil { t.Fatal(err) }
	if len(plan.Capabilities) != 1 || plan.Capabilities[0].RevisionID != "r1" { t.Fatalf("plan=%+v", plan) }
}

func node(id, version, revision string, relations Relations) Node {
	return Node{Selectable: true, Relations: relations, Detail: capability.Detail{
		Descriptor: capability.Descriptor{
			Identity: capability.Identity{ID: id, Kind: capability.KindSkill}, Name: id, Version: version,
			Scope: capability.Scope{Organization: "demo"}, Status: capability.StatusActive,
			Provenance: capability.Provenance{RevisionID: revision, Commit: "commit-" + revision, Tree: "tree-" + revision, ArchiveSHA256TarGZ: "tar-" + revision, ArchiveSHA256ZIP: "zip-" + revision},
		},
		PackageDigests: capability.PackageDigests{TarGZ: "tar-" + revision, ZIP: "zip-" + revision}, MaterializeWith: "materialize_skill",
	}}
}

func capabilityIDs(plan Plan) []string {
	out := make([]string, 0, len(plan.Capabilities))
	for _, item := range plan.Capabilities { out = append(out, item.ID) }
	return out
}
