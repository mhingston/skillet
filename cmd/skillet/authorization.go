package main

import (
	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/config"
)

func configuredAuthorizationPolicy(c config.Config) (authz.Policy, error) {
	if c.Authorization.Mode != "claims" {
		return nil, nil
	}
	grants := make([]authz.Grant, 0, len(c.Authorization.Grants))
	for _, configured := range c.Authorization.Grants {
		grant := authz.Grant{
			Permissions: append([]string(nil), configured.Permissions...),
			Attributes:  make(map[string][]string, len(configured.Attributes)),
		}
		for name, values := range configured.Attributes {
			grant.Attributes[name] = append([]string(nil), values...)
		}
		for _, action := range configured.Actions {
			grant.Actions = append(grant.Actions, authz.Action(action))
		}
		for _, resource := range configured.Resources {
			grant.Resources = append(grant.Resources, authz.ResourceRule{
				Namespace:  resource.Namespace,
				Repository: resource.Repository,
				IDs:        append([]string(nil), resource.IDs...),
			})
		}
		grants = append(grants, grant)
	}
	policy, err := authz.NewClaimsPolicy(grants)
	if err != nil {
		return nil, err
	}
	return policy, nil
}
