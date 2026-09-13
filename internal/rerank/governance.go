package rerank

import (
	"encoding/json"

	"github.com/mhingston/skillet/internal/governance"
)

// MarshalJSON keeps reranker candidate serialization aligned with the routing
// relevance boundary. Governance/control metadata is presentation and policy
// data, never relevance input, so it must not reach a model-backed reranker.
func (c Candidate) MarshalJSON() ([]byte, error) {
	type candidateWire Candidate
	wire := candidateWire(c)
	wire.Metadata = governance.RoutingMetadata(c.Metadata)
	if len(wire.Metadata) == 0 {
		wire.Metadata = nil
	}
	return json.Marshal(wire)
}
