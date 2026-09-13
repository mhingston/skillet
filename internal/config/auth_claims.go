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
	if a.ScopeClaim != "" {
		if err := validateMappedClaimName("auth.scope_claim", a.ScopeClaim); err != nil {
			return err
		}
	}
	if a.RoleClaim != "" {
		if err := validateMappedClaimName("auth.role_claim", a.RoleClaim); err != nil {
			return err
		}
	}
	seenSources := map[string]string{}
	if a.ScopeClaim != "" {
		seenSources[a.ScopeClaim] = "auth.scope_claim"
	}
	if a.RoleClaim != "" {
		if previous, ok := seenSources[a.RoleClaim]; ok {
			return fmt.Errorf("auth.role_claim duplicates claim source configured by %s", previous)
		}
		seenSources[a.RoleClaim] = "auth.role_claim"
	}
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
