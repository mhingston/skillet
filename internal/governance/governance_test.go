package governance

import "testing"

func TestParseGovernanceControlMetadata(t *testing.T) {
	state, metadata, err := Parse(map[string]string{
		DeprecatedKey:  "true",
		ReplacedByKey:  "demo/central/new-skill",
		OwnerKey:       "platform",
		MaintainersKey: "bob, alice, bob",
		ReasonKey:      "successor published",
	}, Defaults{Owner: "fallback", Maintainers: []string{"fallback-maintainer"}})
	if err != nil {
		t.Fatal(err)
	}
	if state != StateDeprecated || metadata.Owner != "platform" || metadata.ReplacedBy != "demo/central/new-skill" || metadata.Reason != "successor published" {
		t.Fatalf("parsed governance = state=%q metadata=%+v", state, metadata)
	}
	if len(metadata.Maintainers) != 2 || metadata.Maintainers[0] != "alice" || metadata.Maintainers[1] != "bob" {
		t.Fatalf("maintainers = %v", metadata.Maintainers)
	}
}

func TestInvalidGovernanceCombinationsAreRejected(t *testing.T) {
	cases := []map[string]string{
		{DeprecatedKey: "true"},
		{ReplacedByKey: "demo/central/new"},
		{StateKey: "active", DeprecatedKey: "true", ReplacedByKey: "demo/central/new"},
		{StateKey: "yanked", ReplacedByKey: "demo/central/new"},
		{StateKey: "unknown"},
		{DeprecatedKey: "yes", ReplacedByKey: "demo/central/new"},
	}
	for _, values := range cases {
		if _, _, err := Parse(values, Defaults{}); err == nil {
			t.Fatalf("values unexpectedly accepted: %+v", values)
		}
	}
}

func TestYankedTransitionIsTerminal(t *testing.T) {
	allowed := [][2]State{{StateActive, StateDeprecated}, {StateDeprecated, StateActive}, {StateDeprecated, StateYanked}, {StateActive, StateYanked}, {StateYanked, StateYanked}}
	for _, transition := range allowed {
		if err := ValidateTransition(transition[0], transition[1]); err != nil {
			t.Fatalf("transition %q -> %q rejected: %v", transition[0], transition[1], err)
		}
	}
	if err := ValidateTransition(StateYanked, StateActive); err == nil {
		t.Fatal("yanked -> active unexpectedly accepted")
	}
}

func TestRoutingMetadataExcludesGovernanceKeys(t *testing.T) {
	got := RoutingMetadata(map[string]string{
		"intent":       "release",
		OwnerKey:        "platform",
		StateKey:        "deprecated",
		ReplacedByKey:   "demo/central/new",
		MaintainersKey:  "alice,bob",
		ReasonKey:       "superseded",
		DeprecatedKey:   "true",
	})
	if len(got) != 1 || got["intent"] != "release" {
		t.Fatalf("routing metadata = %+v", got)
	}
}
