package authorization

import (
	"context"
	"fmt"
	"strings"

	authn "github.com/mhingston/skillet/internal/auth"
)

const ReasonNotEntitled = "not_entitled"

// ResourceRule describes one exact authorization boundary. Empty namespace and
// repository mean organisation-wide. A namespace with no repository covers the
// namespace and its repositories. IDs, when present, further restrict the rule
// to exact stable resource identities.
type ResourceRule struct {
	Namespace  string
	Repository string
	IDs        []string
}

// Grant maps provider-neutral trusted permissions/attributes to existing
// Skillet actions and resource boundaries. Selectors within one grant are ANDed;
// separate grants are ORed. Attribute values within one attribute are ORed.
type Grant struct {
	Permissions []string
	Attributes  map[string][]string
	Actions     []Action
	Resources   []ResourceRule
}

// ClaimsPolicy authorizes only explicitly configured trusted permission and
// attribute combinations. It never sees raw JWT claims or semantic text.
type ClaimsPolicy struct {
	grants []Grant
}

func NewClaimsPolicy(grants []Grant) (ClaimsPolicy, error) {
	if len(grants) == 0 {
		return ClaimsPolicy{}, fmt.Errorf("claims authorization requires at least one grant")
	}
	validated := make([]Grant, 0, len(grants))
	for i, grant := range grants {
		if len(grant.Permissions) == 0 && len(grant.Attributes) == 0 {
			return ClaimsPolicy{}, fmt.Errorf("grant %d requires at least one permission or attribute selector", i)
		}
		if len(grant.Actions) == 0 {
			return ClaimsPolicy{}, fmt.Errorf("grant %d requires at least one action", i)
		}
		if len(grant.Resources) == 0 {
			return ClaimsPolicy{}, fmt.Errorf("grant %d requires at least one resource rule", i)
		}
		copyGrant := Grant{Attributes: map[string][]string{}}
		for _, permission := range grant.Permissions {
			if !validToken(permission) {
				return ClaimsPolicy{}, fmt.Errorf("grant %d has invalid permission %q", i, permission)
			}
			copyGrant.Permissions = append(copyGrant.Permissions, permission)
		}
		for name, values := range grant.Attributes {
			if !validToken(name) || len(values) == 0 {
				return ClaimsPolicy{}, fmt.Errorf("grant %d has invalid attribute selector %q", i, name)
			}
			for _, value := range values {
				if !validValue(value) {
					return ClaimsPolicy{}, fmt.Errorf("grant %d attribute %q has invalid value %q", i, name, value)
				}
				copyGrant.Attributes[name] = append(copyGrant.Attributes[name], value)
			}
		}
		for _, action := range grant.Actions {
			if _, ok := supportedActions[action]; !ok {
				return ClaimsPolicy{}, fmt.Errorf("grant %d has unsupported action %q", i, action)
			}
			copyGrant.Actions = append(copyGrant.Actions, action)
		}
		for j, rule := range grant.Resources {
			if strings.TrimSpace(rule.Namespace) != rule.Namespace || strings.TrimSpace(rule.Repository) != rule.Repository || (rule.Repository != "" && rule.Namespace == "") {
				return ClaimsPolicy{}, fmt.Errorf("grant %d resource %d has invalid namespace/repository scope", i, j)
			}
			copyRule := ResourceRule{Namespace: rule.Namespace, Repository: rule.Repository}
			for _, id := range rule.IDs {
				if !validValue(id) {
					return ClaimsPolicy{}, fmt.Errorf("grant %d resource %d has invalid id %q", i, j, id)
				}
				copyRule.IDs = append(copyRule.IDs, id)
			}
			copyGrant.Resources = append(copyGrant.Resources, copyRule)
		}
		validated = append(validated, copyGrant)
	}
	return ClaimsPolicy{grants: validated}, nil
}

func (p ClaimsPolicy) Authorize(_ context.Context, identity authn.Identity, action Action, resource Resource) Decision {
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
	for _, grant := range p.grants {
		if !grantMatchesIdentity(grant, identity) || !containsAction(grant.Actions, action) {
			continue
		}
		for _, rule := range grant.Resources {
			if ruleMatchesResource(action, rule, resource) {
				return Decision{Allowed: true, Reason: ReasonAllowed}
			}
		}
	}
	return Decision{Reason: ReasonNotEntitled}
}

func grantMatchesIdentity(grant Grant, identity authn.Identity) bool {
	for _, permission := range grant.Permissions {
		if !identity.HasPermission(permission) {
			return false
		}
	}
	for name, allowed := range grant.Attributes {
		actual := identity.Attribute(name)
		matched := false
		for _, value := range actual {
			for _, expected := range allowed {
				if value == expected {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func ruleMatchesResource(action Action, rule ResourceRule, resource Resource) bool {
	if rule.Namespace != "" && rule.Namespace != resource.Namespace {
		return false
	}
	if rule.Repository != "" && rule.Repository != resource.Repository {
		return false
	}
	if len(rule.IDs) == 0 {
		return true
	}
	// Search authorization has two phases. An empty ID admits the caller to
	// search an already-entitled scope; exact ID rules are then applied to every
	// ranked candidate before disclosure. Non-search operations always require
	// the exact stable resource identity when IDs are configured.
	if resource.ID == "" && (action == ActionCapabilitySearch || action == ActionKnowledgeSearch) {
		return true
	}
	for _, id := range rule.IDs {
		if resource.ID == id {
			return true
		}
	}
	return false
}

func containsAction(actions []Action, action Action) bool {
	for _, allowed := range actions {
		if allowed == action {
			return true
		}
	}
	return false
}

func validToken(value string) bool {
	return validValue(value) && !strings.ContainsAny(value, " \t\r\n")
}

func validValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}
