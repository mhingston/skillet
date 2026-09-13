package main

import (
	"testing"

	"github.com/mhingston/skillet/internal/governance"
)

func TestCapabilityMetadataKeysPreserveGovernanceControlFields(t *testing.T) {
	if got := capabilityMetadataKeys(nil); got != nil {
		t.Fatalf("nil semantic allow-list should remain unfiltered, got %v", got)
	}
	got := capabilityMetadataKeys([]string{"intent", governance.StateKey})
	want := []string{
		"intent",
		governance.StateKey,
		governance.OwnerKey,
		governance.MaintainersKey,
		governance.ReasonKey,
		governance.DeprecatedKey,
		governance.ReplacedByKey,
	}
	seen := map[string]int{}
	for _, key := range got {
		seen[key]++
	}
	for _, key := range want {
		if seen[key] != 1 {
			t.Fatalf("key %q count = %d in %v", key, seen[key], got)
		}
	}
}
