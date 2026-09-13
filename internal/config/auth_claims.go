package config

import (
	"errors"
	"fmt"
	"strings"
)

func (a Auth) validateClaimMapping() error {
	configured := a.ScopeClaim != "" || a.RoleClaim != "" || len(a.AttributeClaims) > 0
	if !configured {
		return nil
	}
	if a.Mode != "oidc" {
		return errors.New("auth claim mappings are only valid in oidc mode")
	}

	organizationClaim := a.OrganizationClaim
	if organizationClaim == "" {
		organizationClaim = "organization_id"
	}
	scopeClaim := a.ScopeClaim
	if scopeClaim == "" {
		scopeClaim = "scope"
	}
	roleClaim := a.RoleClaim
	if roleClaim == "" {
		roleClaim = "roles"
	}

	if err := validateMappedClaimName("auth.organization_claim", organizationClaim); err != nil {
		return err
	}
	if err := validateMappedClaimName("auth.scope_claim", scopeClaim); err != nil {
		return err
	}
	if err := validateMappedClaimName("auth.role_claim", roleClaim); err != nil {
		return err
	}

	seenSources := map[string]string{organizationClaim: "auth.organization_claim"}
	if previous, ok := seenSources[scopeClaim]; ok {
		return fmt.Errorf("auth.scope_claim duplicates claim source configured by %s", previous)
	}
	seenSources[scopeClaim] = "auth.scope_claim"
	if scopeClaim == "scope" {
		// `scp` is the documented fallback source for the default delegated scope
		// mapping and therefore belongs to the same normalization slot.
		if previous, ok := seenSources["scp"]; ok {
			return fmt.Errorf("auth.scope_claim fallback duplicates claim source configured by %s", previous)
		}
		seenSources["scp"] = "auth.scope_claim fallback"
	}
	if previous, ok := seenSources[roleClaim]; ok {
		return fmt.Errorf("auth.role_claim duplicates claim source configured by %s", previous)
	}
	seenSources[roleClaim] = "auth.role_claim"

	for name, claim := range a.AttributeClaims {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, " \t\r\n") {
			return fmt.Errorf("auth.attribute_claims key %q must be a non-empty token", name)
		}
		field := fmt.Sprintf("auth.attribute_claims[%q]", name)
		if err := validateMappedClaimName(field, claim); err != nil {
			return err
		}
		if previous, ok := seenSources[claim]; ok {
			return fmt.Errorf("%s duplicates claim source configured by %s", field, previous)
		}
		seenSources[claim] = field
	}
	return nil
}

func validateMappedClaimName(field, value string) error {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("%s must be a non-empty claim-name token", field)
	}
	return nil
}
