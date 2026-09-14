// Package authorization owns Skillet's transport-neutral request authorization contract.
//
// Authentication remains in internal/auth. Policies consume only identities that have
// already crossed that trust boundary plus the minimum authoritative resource scope
// needed to decide whether an existing Skillet operation is allowed.
package authorization

import (
	"context"
	"strings"

	authn "github.com/mhingston/skillet/internal/auth"
)

// Action identifies one existing Skillet operation that can require authorization.
type Action string

const (
	ActionCapabilitySearch      Action = "capability.search"
	ActionCapabilityDescribe    Action = "capability.describe"
	ActionCapabilityMaterialize Action = "capability.materialize"
	ActionKnowledgeSearch       Action = "knowledge.search"
	ActionKnowledgeRead         Action = "knowledge.read"
	ActionEvidenceReport        Action = "evidence.report"
	ActionEvidenceReview        Action = "evidence.review"
	ActionGovernanceRead        Action = "governance.read"
	ActionOperatorRead          Action = "operator.read"
	ActionOperatorAuditProbe    Action = "operator.audit_probe"
)

var supportedActions = map[Action]struct{}{
	ActionCapabilitySearch:      {},
	ActionCapabilityDescribe:    {},
	ActionCapabilityMaterialize: {},
	ActionKnowledgeSearch:       {},
	ActionKnowledgeRead:         {},
	ActionEvidenceReport:        {},
	ActionEvidenceReview:        {},
	ActionGovernanceRead:        {},
	ActionOperatorRead:          {},
	ActionOperatorAuditProbe:    {},
}

// Resource contains authorization scope only. It deliberately contains no
// semantic query/ranking text. ID is an optional stable capability, knowledge,
// evidence, governance, or operator resource identity when an operation
// addresses one resource.
type Resource struct {
	OrganizationID string
	Namespace      string
	Repository     string
	ID             string
}

// Decision is intentionally small and deterministic. Reason is a bounded
// machine-oriented value suitable for tests, logs, and audit metadata.
type Decision struct {
	Allowed bool
	Reason  string
}

const (
	ReasonAllowed              = "allowed"
	ReasonUnsupportedAction    = "unsupported_action"
	ReasonInvalidIdentity      = "invalid_identity"
	ReasonInvalidResource      = "invalid_resource"
	ReasonOrganizationMismatch = "organization_mismatch"
)

// Policy authorizes one already-authenticated identity to perform one action
// against one resource. Implementations must not rely on HTTP, MCP, or provider
// specific token claim names.
type Policy interface {
	Authorize(context.Context, authn.Identity, Action, Resource) Decision
}

// CompatibilityPolicy preserves the pre-M2 authorization contract: a trusted
// authenticated identity may act only inside its exact organization. Namespace
// and repository entitlements are intentionally added by later opt-in policies;
// they are not inferred here.
type CompatibilityPolicy struct{}

func (CompatibilityPolicy) Authorize(_ context.Context, identity authn.Identity, action Action, resource Resource) Decision {
	if _, ok := supportedActions[action]; !ok {
		return Decision{Reason: ReasonUnsupportedAction}
	}
	if strings.TrimSpace(identity.Subject) == "" || strings.TrimSpace(identity.OrganizationID) == "" {
		return Decision{Reason: ReasonInvalidIdentity}
	}
	if strings.TrimSpace(resource.OrganizationID) == "" || (resource.Repository != "" && strings.TrimSpace(resource.Namespace) == "") {
		return Decision{Reason: ReasonInvalidResource}
	}
	if identity.OrganizationID != resource.OrganizationID {
		return Decision{Reason: ReasonOrganizationMismatch}
	}
	return Decision{Allowed: true, Reason: ReasonAllowed}
}
