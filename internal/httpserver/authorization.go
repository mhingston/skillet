package httpserver

import (
	"context"
	"errors"
	"fmt"
	"sync"

	authz "github.com/mhingston/skillet/internal/authorization"
)

var (
	ErrAuthorizationDenied = errors.New("authorization denied")
	authorizationPolicies  sync.Map // map[*Server]authorization.Policy
)

// ConfigureAuthorization attaches an opt-in authorization policy to the server.
// A nil policy preserves the existing compatibility behaviour exactly.
func (s *Server) ConfigureAuthorization(policy authz.Policy) {
	if s == nil {
		return
	}
	if policy == nil {
		authorizationPolicies.Delete(s)
		return
	}
	authorizationPolicies.Store(s, policy)
}

func authorizationPolicyFor(s *Server) authz.Policy {
	if s == nil {
		return nil
	}
	value, ok := authorizationPolicies.Load(s)
	if !ok {
		return nil
	}
	policy, _ := value.(authz.Policy)
	return policy
}

// authorize enforces only an explicitly configured policy. Existing
// compatibility/development behaviour therefore remains unchanged when claims
// authorization is not enabled.
func (s *Server) authorize(ctx context.Context, action authz.Action, resource authz.Resource) error {
	policy := authorizationPolicyFor(s)
	if policy == nil {
		return nil
	}
	identity, ok := Identity(ctx)
	if !ok {
		decision := authz.Decision{Reason: authz.ReasonInvalidIdentity}
		s.recordAuthorizationDenial(ctx, action, resource, decision)
		return fmt.Errorf("%w: %s", ErrAuthorizationDenied, decision.Reason)
	}
	if resource.OrganizationID == "" {
		resource.OrganizationID = identity.OrganizationID
	}
	decision := policy.Authorize(ctx, identity, action, resource)
	if decision.Allowed {
		return nil
	}
	s.recordAuthorizationDenial(ctx, action, resource, decision)
	return fmt.Errorf("%w: %s", ErrAuthorizationDenied, decision.Reason)
}

func (s *Server) recordAuthorizationDenial(ctx context.Context, action authz.Action, resource authz.Resource, decision authz.Decision) {
	organizationID := resource.OrganizationID
	if organizationID == "" {
		if identity, ok := Identity(ctx); ok {
			organizationID = identity.OrganizationID
		} else {
			organizationID = s.organizationID
		}
	}
	details := map[string]any{
		"action": string(action),
		"reason": decision.Reason,
	}
	if resource.Namespace != "" {
		details["namespace"] = resource.Namespace
	}
	if resource.Repository != "" {
		details["repository"] = resource.Repository
	}
	if resource.ID != "" {
		details["resource_id"] = resource.ID
	}
	_ = s.recordAudit(ctx, organizationID, "authorization_denied", details)
}
