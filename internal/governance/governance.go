// Package governance owns catalogue control metadata that must not affect
// semantic relevance. It deliberately contains no retrieval or execution code.
package governance

import (
	"fmt"
	"sort"
	"strings"
)

type State string

const (
	StateActive     State = "active"
	StateDeprecated State = "deprecated"
	StateYanked     State = "yanked"
)

type Visibility string

const (
	VisibilityOrganization Visibility = "organization"
	VisibilityNamespace    Visibility = "namespace"
	VisibilityRepository   Visibility = "repository"
)

const (
	StateKey       = "skillet.governance.state"
	OwnerKey       = "skillet.governance.owner"
	MaintainersKey = "skillet.governance.maintainers"
	ReasonKey      = "skillet.governance.reason"
	DeprecatedKey  = "skillet.deprecated"
	ReplacedByKey  = "skillet.replaced_by"
)

type Defaults struct {
	Owner       string
	Maintainers []string
}

type Metadata struct {
	Owner               string     `json:"owner,omitempty"`
	Maintainers         []string   `json:"maintainers,omitempty"`
	Visibility          Visibility `json:"visibility"`
	ReplacedBy          string     `json:"replaced_by,omitempty"`
	ReplacementResolved bool       `json:"replacement_resolved,omitempty"`
	Reason              string     `json:"reason,omitempty"`
}

// Parse projects reserved publisher control metadata into explicit governance
// state. Reserved keys are never intended to become routing text.
func Parse(values map[string]string, defaults Defaults) (State, Metadata, error) {
	state := StateActive
	if raw := strings.TrimSpace(values[StateKey]); raw != "" {
		state = State(raw)
		if !ValidState(state) {
			return "", Metadata{}, fmt.Errorf("unsupported governance state %q", raw)
		}
	}

	deprecated := strings.TrimSpace(values[DeprecatedKey])
	if deprecated != "" {
		switch deprecated {
		case "true":
			if explicit := strings.TrimSpace(values[StateKey]); explicit != "" && state != StateDeprecated {
				return "", Metadata{}, fmt.Errorf("%s=true conflicts with governance state %q", DeprecatedKey, state)
			}
			state = StateDeprecated
		case "false":
			if explicit := strings.TrimSpace(values[StateKey]); explicit != "" && state == StateDeprecated {
				return "", Metadata{}, fmt.Errorf("%s=false conflicts with governance state %q", DeprecatedKey, state)
			}
		default:
			return "", Metadata{}, fmt.Errorf("%s must be true or false", DeprecatedKey)
		}
	}

	replacedBy := strings.TrimSpace(values[ReplacedByKey])
	if deprecated == "true" && replacedBy == "" {
		return "", Metadata{}, fmt.Errorf("%s=true requires %s", DeprecatedKey, ReplacedByKey)
	}
	if replacedBy != "" && state != StateDeprecated {
		return "", Metadata{}, fmt.Errorf("%s requires deprecated governance state", ReplacedByKey)
	}

	owner := strings.TrimSpace(defaults.Owner)
	if configured := strings.TrimSpace(values[OwnerKey]); configured != "" {
		owner = configured
	}
	maintainers := normalizeMaintainers(defaults.Maintainers)
	if raw, exists := values[MaintainersKey]; exists {
		maintainers = normalizeMaintainers(strings.Split(raw, ","))
		if strings.TrimSpace(raw) != "" && len(maintainers) == 0 {
			return "", Metadata{}, fmt.Errorf("%s must contain at least one maintainer", MaintainersKey)
		}
	}

	return state, Metadata{
		Owner:       owner,
		Maintainers: maintainers,
		ReplacedBy:  replacedBy,
		Reason:      strings.TrimSpace(values[ReasonKey]),
	}, nil
}

func ValidState(state State) bool {
	return state == StateActive || state == StateDeprecated || state == StateYanked
}

// ValidateTransition defines the small M1 lifecycle state machine. Yank is
// terminal for a revision so an unavailable revision cannot silently become a
// new-selection candidate again; historical package restoration is separate.
func ValidateTransition(from, to State) error {
	if !ValidState(from) || !ValidState(to) {
		return fmt.Errorf("invalid governance transition %q -> %q", from, to)
	}
	if from == StateYanked && to != StateYanked {
		return fmt.Errorf("yanked governance state is terminal")
	}
	return nil
}

func IsControlKey(key string) bool {
	switch key {
	case StateKey, OwnerKey, MaintainersKey, ReasonKey, DeprecatedKey, ReplacedByKey:
		return true
	default:
		return false
	}
}

// RoutingMetadata returns only semantic metadata. Governance/control values
// are intentionally excluded so owner, state, scope and successor guidance do
// not alter lexical/vector relevance.
func RoutingMetadata(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		if !IsControlKey(key) {
			out[key] = value
		}
	}
	return out
}

func normalizeMaintainers(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
