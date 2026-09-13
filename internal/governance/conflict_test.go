package governance

import "testing"

func TestParseRejectsDeprecatedFalseWithExplicitDeprecatedState(t *testing.T) {
	_, _, err := Parse(map[string]string{
		StateKey:      string(StateDeprecated),
		DeprecatedKey: "false",
	}, Defaults{})
	if err == nil {
		t.Fatal("contradictory explicit deprecated state and skillet.deprecated=false was accepted")
	}
}
