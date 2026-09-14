package httpserver

import (
	"sync"

	"github.com/mhingston/skillet/internal/knowledge"
	"github.com/mhingston/skillet/internal/knowledgemcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// knowledgeServices keeps the vNext knowledge domain attached to the existing
// Skillet server without merging knowledge state into capability/skill state.
// It is process-local transport composition only; the knowledge service remains
// the owner of retrieval, provenance, and reconciliation semantics.
var knowledgeServices sync.Map // map[*Server]*knowledge.Service

// ConfigureKnowledge attaches one knowledge service to this Skillet server.
// Passing nil removes the attachment and preserves the v1-only MCP surface.
func (s *Server) ConfigureKnowledge(service *knowledge.Service) {
	if s == nil {
		return
	}
	if service == nil {
		knowledgeServices.Delete(s)
		return
	}
	knowledgeServices.Store(s, service)
}

func addKnowledgeTools(server *mcp.Server, app *Server) {
	if server == nil || app == nil {
		return
	}
	// Capability, deterministic composition, evidence, reviewable proposals, and
	// knowledge are separate domains but share this bounded MCP transport
	// composition point. Proposal tools produce/accept review data only; they do
	// not execute patches or mutate canonical source.
	addCapabilityTools(server, app)
	addCompositionTools(server, app)
	addEvidenceTools(server, app)
	addProposalTools(server, app)
	value, ok := knowledgeServices.Load(app)
	if !ok {
		return
	}
	service, ok := value.(*knowledge.Service)
	if !ok || service == nil {
		return
	}
	knowledgemcp.AddTools(server, service, app.authorize)
}
