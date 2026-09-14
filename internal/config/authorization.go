package config

import (
	"fmt"
	"strings"
)

type Authorization struct {
	Mode   string               `yaml:"mode"`
	Grants []AuthorizationGrant `yaml:"grants"`
}

type AuthorizationGrant struct {
	Permissions []string                    `yaml:"permissions"`
	Attributes  map[string][]string         `yaml:"attributes"`
	Actions     []string                    `yaml:"actions"`
	Resources   []AuthorizationResourceRule `yaml:"resources"`
}

type AuthorizationResourceRule struct {
	Namespace  string   `yaml:"namespace"`
	Repository string   `yaml:"repository"`
	IDs        []string `yaml:"ids"`
}

func (a *Authorization) validate(auth Auth) error {
	if a.Mode == "" {
		a.Mode = "compatibility"
	}
	if a.Mode != "compatibility" && a.Mode != "claims" {
		return fmt.Errorf("authorization.mode %q must be compatibility or claims", a.Mode)
	}
	if a.Mode == "compatibility" {
		if len(a.Grants) != 0 {
			return fmt.Errorf("authorization.grants require authorization.mode claims")
		}
		return nil
	}
	if auth.Mode != "oidc" {
		return fmt.Errorf("authorization.mode claims requires auth.mode oidc")
	}
	if len(a.Grants) == 0 {
		return fmt.Errorf("authorization.mode claims requires at least one grant")
	}
	for i, grant := range a.Grants {
		if len(grant.Permissions) == 0 && len(grant.Attributes) == 0 {
			return fmt.Errorf("authorization.grants[%d] requires permissions or attributes", i)
		}
		if len(grant.Actions) == 0 {
			return fmt.Errorf("authorization.grants[%d].actions is required", i)
		}
		if len(grant.Resources) == 0 {
			return fmt.Errorf("authorization.grants[%d].resources is required", i)
		}
		for _, permission := range grant.Permissions {
			if !validAuthorizationToken(permission) {
				return fmt.Errorf("authorization.grants[%d] has invalid permission %q", i, permission)
			}
		}
		for name, values := range grant.Attributes {
			if !validAuthorizationToken(name) || len(values) == 0 {
				return fmt.Errorf("authorization.grants[%d] has invalid attribute %q", i, name)
			}
			if _, mapped := auth.AttributeClaims[name]; !mapped {
				return fmt.Errorf("authorization.grants[%d] attribute %q is not mapped by auth.attribute_claims", i, name)
			}
			for _, value := range values {
				if !validAuthorizationValue(value) {
					return fmt.Errorf("authorization.grants[%d] attribute %q has invalid value %q", i, name, value)
				}
			}
		}
		for j, resource := range grant.Resources {
			if strings.TrimSpace(resource.Namespace) != resource.Namespace || strings.TrimSpace(resource.Repository) != resource.Repository || (resource.Repository != "" && resource.Namespace == "") {
				return fmt.Errorf("authorization.grants[%d].resources[%d] has invalid namespace/repository scope", i, j)
			}
			for _, id := range resource.IDs {
				if !validAuthorizationValue(id) {
					return fmt.Errorf("authorization.grants[%d].resources[%d] has invalid id %q", i, j, id)
				}
			}
		}
	}
	return nil
}

func validAuthorizationToken(value string) bool {
	return validAuthorizationValue(value) && !strings.ContainsAny(value, " \t\r\n")
}

func validAuthorizationValue(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}
