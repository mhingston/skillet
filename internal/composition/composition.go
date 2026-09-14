// Package composition resolves explicitly declared capability dependencies and
// curated collections. It is intentionally side-effect free: callers supply an
// already-authorized, governance-filtered snapshot and receive a deterministic
// immutable resolution plan. The package never searches semantically, executes
// capabilities, or infers undeclared edges.
package composition

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"github.com/mhingston/skillet/internal/capability"
)

const (
	MetadataRequires   = "skillet.requires"
	MetadataRecommends = "skillet.recommends"
	MetadataConflicts  = "skillet.conflicts"
)

type Reference struct {
	ID         string          `json:"id" yaml:"id"`
	RevisionID string          `json:"revision_id,omitempty" yaml:"revision_id,omitempty"`
	Version    string          `json:"version,omitempty" yaml:"version,omitempty"`
	Kind       capability.Kind `json:"kind,omitempty" yaml:"kind,omitempty"`
	Namespace  string          `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Repository string          `json:"repository,omitempty" yaml:"repository,omitempty"`
}

type Relations struct {
	Requires   []Reference `json:"requires,omitempty"`
	Recommends []Reference `json:"recommends,omitempty"`
	Conflicts  []Reference `json:"conflicts,omitempty"`
}

type Collection struct {
	ID          string      `json:"id" yaml:"id"`
	Name        string      `json:"name" yaml:"name"`
	Description string      `json:"description,omitempty" yaml:"description,omitempty"`
	Members     []Reference `json:"members" yaml:"members"`
}

type Node struct {
	Detail      capability.Detail `json:"detail"`
	Relations   Relations         `json:"relations,omitempty"`
	Selectable bool              `json:"selectable"`
}

type Snapshot struct {
	Nodes       []Node       `json:"nodes"`
	Collections []Collection `json:"collections,omitempty"`
}

type Selection struct {
	Capability *Reference `json:"capability,omitempty"`
	Collection string     `json:"collection,omitempty"`
}

type Edge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Relation   string `json:"relation"`
	Constraint string `json:"constraint,omitempty"`
	Reason     string `json:"reason"`
}

type LockedCapability struct {
	ID                   string          `json:"id"`
	Kind                 capability.Kind `json:"kind"`
	Name                 string          `json:"name"`
	Version              string          `json:"version,omitempty"`
	RevisionID           string          `json:"revision_id"`
	Commit               string          `json:"commit,omitempty"`
	Tree                 string          `json:"tree,omitempty"`
	ArchiveSHA256TarGZ   string          `json:"archive_sha256_tar_gz,omitempty"`
	ArchiveSHA256ZIP     string          `json:"archive_sha256_zip,omitempty"`
	MaterializeWith      string          `json:"materialize_with,omitempty"`
	Status               capability.Status `json:"status"`
}

type Recommendation struct {
	From      string    `json:"from"`
	Reference Reference `json:"reference"`
}

type ConflictDeclaration struct {
	From      string    `json:"from"`
	Reference Reference `json:"reference"`
}

type Plan struct {
	Selection       Selection             `json:"selection"`
	Capabilities    []LockedCapability    `json:"capabilities"`
	Edges           []Edge                `json:"edges"`
	Recommendations []Recommendation      `json:"recommendations,omitempty"`
	Conflicts       []ConflictDeclaration `json:"conflicts,omitempty"`
}

type ErrorCode string

const (
	ErrorInvalidSelection ErrorCode = "invalid_selection"
	ErrorUnavailable      ErrorCode = "unavailable"
	ErrorUnsatisfiable    ErrorCode = "unsatisfiable_version"
	ErrorCycle            ErrorCode = "cycle"
	ErrorConflict         ErrorCode = "conflict"
)

type ResolveError struct {
	Code ErrorCode
	Msg  string
}

func (e *ResolveError) Error() string { return e.Msg }

func IsCode(err error, code ErrorCode) bool {
	var target *ResolveError
	return errors.As(err, &target) && target.Code == code
}

// ParseMetadata decodes the three reserved composition keys from the existing
// string-valued Agent Skills metadata map. Values are JSON arrays so the
// ordinary metadata contract remains backwards compatible and round-trips
// without introducing non-standard top-level SKILL.md fields.
func ParseMetadata(metadata map[string]string) (Relations, error) {
	var out Relations
	for key, destination := range map[string]*[]Reference{
		MetadataRequires: &out.Requires,
		MetadataRecommends: &out.Recommends,
		MetadataConflicts: &out.Conflicts,
	} {
		raw, ok := metadata[key]
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		if err := json.Unmarshal([]byte(raw), destination); err != nil {
			return Relations{}, fmt.Errorf("%s must be a JSON array of capability references: %w", key, err)
		}
		for i, ref := range *destination {
			if err := ValidateReference(ref); err != nil {
				return Relations{}, fmt.Errorf("%s[%d]: %w", key, i, err)
			}
		}
	}
	return out, nil
}

func ValidateReference(ref Reference) error {
	if strings.TrimSpace(ref.ID) == "" || ref.ID != strings.TrimSpace(ref.ID) {
		return fmt.Errorf("id is required and must not contain surrounding whitespace")
	}
	if ref.RevisionID != "" && ref.Version != "" {
		return fmt.Errorf("revision_id and version are mutually exclusive")
	}
	if ref.Kind != "" && ref.Kind != capability.KindSkill && ref.Kind != capability.KindPlaybook && ref.Kind != capability.KindTool {
		return fmt.Errorf("unsupported capability kind %q", ref.Kind)
	}
	if ref.Repository != "" && ref.Namespace == "" {
		return fmt.Errorf("repository constraint requires namespace")
	}
	if ref.Version != "" {
		if _, err := semver.StrictNewVersion(ref.Version); err != nil {
			if _, constraintErr := semver.NewConstraint(ref.Version); constraintErr != nil {
				return fmt.Errorf("version must be valid SemVer or a SemVer constraint: %v", constraintErr)
			}
		}
	}
	return nil
}

func ValidateCollection(collection Collection) error {
	if strings.TrimSpace(collection.ID) == "" || collection.ID != strings.TrimSpace(collection.ID) {
		return fmt.Errorf("collection id is required and must not contain surrounding whitespace")
	}
	if strings.TrimSpace(collection.Name) == "" {
		return fmt.Errorf("collection %q name is required", collection.ID)
	}
	if len(collection.Members) == 0 {
		return fmt.Errorf("collection %q requires at least one member", collection.ID)
	}
	for i, member := range collection.Members {
		if err := ValidateReference(member); err != nil {
			return fmt.Errorf("collection %q member[%d]: %w", collection.ID, i, err)
		}
	}
	return nil
}

type requirement struct {
	Ref      Reference
	From     string
	Relation string
	Reason   string
}

type resolver struct {
	byIdentity  map[string][]Node
	byRevision  map[string]Node
	collections map[string]Collection
}

type state struct {
	requirements map[string][]requirement
	selected     map[string]Node
}

func Resolve(snapshot Snapshot, selection Selection) (Plan, error) {
	r, err := newResolver(snapshot)
	if err != nil {
		return Plan{}, err
	}
	st := state{requirements: map[string][]requirement{}, selected: map[string]Node{}}
	if (selection.Capability == nil) == (selection.Collection == "") {
		return Plan{}, &ResolveError{Code: ErrorInvalidSelection, Msg: "select exactly one capability or collection"}
	}
	if selection.Capability != nil {
		if err := ValidateReference(*selection.Capability); err != nil {
			return Plan{}, &ResolveError{Code: ErrorInvalidSelection, Msg: err.Error()}
		}
		addRequirement(&st, requirement{Ref: *selection.Capability, From: "$selection", Relation: "selected", Reason: "explicit selection"})
	} else {
		collection, ok := r.collections[selection.Collection]
		if !ok {
			return Plan{}, &ResolveError{Code: ErrorUnavailable, Msg: "collection is unavailable"}
		}
		for _, member := range sortedReferences(collection.Members) {
			addRequirement(&st, requirement{Ref: member, From: "collection:" + collection.ID, Relation: "member", Reason: "curated collection member"})
		}
	}

	resolved, lastErr := r.solve(st)
	if resolved == nil {
		if lastErr != nil {
			return Plan{}, lastErr
		}
		return Plan{}, &ResolveError{Code: ErrorUnavailable, Msg: "capability dependency is unavailable"}
	}
	return r.plan(selection, *resolved)
}

func newResolver(snapshot Snapshot) (*resolver, error) {
	r := &resolver{byIdentity: map[string][]Node{}, byRevision: map[string]Node{}, collections: map[string]Collection{}}
	for _, node := range snapshot.Nodes {
		d := node.Detail.Descriptor
		if strings.TrimSpace(d.Identity.ID) == "" || strings.TrimSpace(d.Provenance.RevisionID) == "" {
			return nil, fmt.Errorf("composition node requires stable identity and immutable revision")
		}
		if _, exists := r.byRevision[d.Provenance.RevisionID]; exists {
			return nil, fmt.Errorf("duplicate composition revision %q", d.Provenance.RevisionID)
		}
		if _, err := ParseMetadata(d.Metadata); err != nil && emptyRelations(node.Relations) {
			return nil, fmt.Errorf("capability %q composition metadata: %w", d.Identity.ID, err)
		} else if emptyRelations(node.Relations) {
			node.Relations, _ = ParseMetadata(d.Metadata)
		}
		r.byIdentity[d.Identity.ID] = append(r.byIdentity[d.Identity.ID], node)
		r.byRevision[d.Provenance.RevisionID] = node
	}
	for id := range r.byIdentity {
		sort.SliceStable(r.byIdentity[id], func(i, j int) bool { return nodeLess(r.byIdentity[id][j], r.byIdentity[id][i]) })
	}
	for _, collection := range snapshot.Collections {
		if err := ValidateCollection(collection); err != nil {
			return nil, err
		}
		if _, exists := r.collections[collection.ID]; exists {
			return nil, fmt.Errorf("duplicate collection %q", collection.ID)
		}
		r.collections[collection.ID] = collection
	}
	return r, nil
}

func emptyRelations(rel Relations) bool {
	return len(rel.Requires) == 0 && len(rel.Recommends) == 0 && len(rel.Conflicts) == 0
}

func (r *resolver) solve(st state) (*state, error) {
	identity := nextUnresolved(st)
	if identity == "" {
		copy := cloneState(st)
		return &copy, nil
	}
	reqs := st.requirements[identity]
	candidates := r.matchingCandidates(identity, reqs)
	if len(candidates) == 0 {
		if len(r.byIdentity[identity]) == 0 {
			return nil, &ResolveError{Code: ErrorUnavailable, Msg: "capability dependency is unavailable"}
		}
		return nil, &ResolveError{Code: ErrorUnsatisfiable, Msg: "no selectable immutable revision satisfies all declared constraints"}
	}
	var lastErr error
	for _, candidate := range candidates {
		branch := cloneState(st)
		branch.selected[identity] = candidate
		valid := true
		for _, dep := range sortedReferences(candidate.Relations.Requires) {
			req := requirement{Ref: dep, From: identity, Relation: "requires", Reason: "declared required dependency"}
			addRequirement(&branch, req)
			if selected, ok := branch.selected[dep.ID]; ok && !nodeMatches(selected, branch.requirements[dep.ID]) {
				valid = false
				lastErr = &ResolveError{Code: ErrorUnsatisfiable, Msg: "no selectable immutable revision satisfies all declared constraints"}
				break
			}
		}
		if !valid {
			continue
		}
		resolved, err := r.solve(branch)
		if err == nil {
			return resolved, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func nextUnresolved(st state) string {
	ids := make([]string, 0, len(st.requirements))
	for id := range st.requirements {
		if _, selected := st.selected[id]; !selected {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func (r *resolver) matchingCandidates(identity string, reqs []requirement) []Node {
	out := make([]Node, 0, len(r.byIdentity[identity]))
	for _, node := range r.byIdentity[identity] {
		if !node.Selectable || !nodeMatches(node, reqs) {
			continue
		}
		out = append(out, node)
	}
	return out
}

func nodeMatches(node Node, reqs []requirement) bool {
	d := node.Detail.Descriptor
	for _, req := range reqs {
		ref := req.Ref
		if ref.ID != d.Identity.ID {
			return false
		}
		if ref.RevisionID != "" && ref.RevisionID != d.Provenance.RevisionID {
			return false
		}
		if ref.Kind != "" && ref.Kind != d.Identity.Kind {
			return false
		}
		if ref.Namespace != "" && ref.Namespace != d.Scope.Namespace {
			return false
		}
		if ref.Repository != "" && ref.Repository != d.Scope.Repository {
			return false
		}
		if ref.Version != "" && !versionMatches(d.Version, ref.Version) {
			return false
		}
	}
	return true
}

func versionMatches(version, selector string) bool {
	if version == "" {
		return false
	}
	v, err := semver.StrictNewVersion(version)
	if err != nil {
		return false
	}
	if v.Prerelease() != "" && !strings.Contains(selector, "-") {
		return false
	}
	if exact, err := semver.StrictNewVersion(selector); err == nil {
		return v.Equal(exact)
	}
	constraint, err := semver.NewConstraint(selector)
	return err == nil && constraint.Check(v)
}

func nodeLess(a, b Node) bool {
	av, aerr := semver.StrictNewVersion(a.Detail.Descriptor.Version)
	bv, berr := semver.StrictNewVersion(b.Detail.Descriptor.Version)
	switch {
	case aerr == nil && berr == nil:
		if !av.Equal(bv) {
			return av.LessThan(bv)
		}
	case aerr == nil:
		return false
	case berr == nil:
		return true
	}
	return a.Detail.Descriptor.Provenance.RevisionID < b.Detail.Descriptor.Provenance.RevisionID
}

func addRequirement(st *state, req requirement) {
	st.requirements[req.Ref.ID] = append(st.requirements[req.Ref.ID], req)
}

func cloneState(in state) state {
	out := state{requirements: make(map[string][]requirement, len(in.requirements)), selected: make(map[string]Node, len(in.selected))}
	for id, reqs := range in.requirements {
		out.requirements[id] = append([]requirement(nil), reqs...)
	}
	for id, node := range in.selected {
		out.selected[id] = node
	}
	return out
}

func (r *resolver) plan(selection Selection, st state) (Plan, error) {
	if cycle := requiredCycle(st.selected); len(cycle) > 0 {
		return Plan{}, &ResolveError{Code: ErrorCycle, Msg: "declared capability dependencies contain a cycle: " + strings.Join(cycle, " -> ")}
	}
	if from, ref, ok := firstConflict(st.selected); ok {
		return Plan{}, &ResolveError{Code: ErrorConflict, Msg: fmt.Sprintf("selected capabilities conflict (%s declares a conflict with %s)", from, ref.ID)}
	}

	plan := Plan{Selection: selection}
	order := topologicalOrder(st.selected)
	for _, id := range order {
		node := st.selected[id]
		d := node.Detail.Descriptor
		locked := LockedCapability{
			ID: d.Identity.ID, Kind: d.Identity.Kind, Name: d.Name, Version: d.Version,
			RevisionID: d.Provenance.RevisionID, Commit: d.Provenance.Commit, Tree: d.Provenance.Tree,
			ArchiveSHA256TarGZ: firstNonEmpty(node.Detail.PackageDigests.TarGZ, d.Provenance.ArchiveSHA256TarGZ),
			ArchiveSHA256ZIP: firstNonEmpty(node.Detail.PackageDigests.ZIP, d.Provenance.ArchiveSHA256ZIP),
			MaterializeWith: node.Detail.MaterializeWith, Status: d.Status,
		}
		plan.Capabilities = append(plan.Capabilities, locked)
		for _, ref := range sortedReferences(node.Relations.Recommends) {
			plan.Recommendations = append(plan.Recommendations, Recommendation{From: id, Reference: ref})
		}
		for _, ref := range sortedReferences(node.Relations.Conflicts) {
			plan.Conflicts = append(plan.Conflicts, ConflictDeclaration{From: id, Reference: ref})
		}
	}

	seenEdges := map[string]bool{}
	for target, reqs := range st.requirements {
		if _, ok := st.selected[target]; !ok {
			continue
		}
		for _, req := range reqs {
			constraint := req.Ref.Version
			if req.Ref.RevisionID != "" {
				constraint = "revision:" + req.Ref.RevisionID
			}
			edge := Edge{From: req.From, To: target, Relation: req.Relation, Constraint: constraint, Reason: req.Reason}
			key := edge.From + "\x00" + edge.To + "\x00" + edge.Relation + "\x00" + edge.Constraint + "\x00" + edge.Reason
			if !seenEdges[key] {
				plan.Edges = append(plan.Edges, edge)
				seenEdges[key] = true
			}
		}
	}
	sort.Slice(plan.Edges, func(i, j int) bool {
		a, b := plan.Edges[i], plan.Edges[j]
		if a.From != b.From { return a.From < b.From }
		if a.To != b.To { return a.To < b.To }
		if a.Relation != b.Relation { return a.Relation < b.Relation }
		if a.Constraint != b.Constraint { return a.Constraint < b.Constraint }
		return a.Reason < b.Reason
	})
	sort.Slice(plan.Recommendations, func(i, j int) bool {
		if plan.Recommendations[i].From != plan.Recommendations[j].From { return plan.Recommendations[i].From < plan.Recommendations[j].From }
		return referenceKey(plan.Recommendations[i].Reference) < referenceKey(plan.Recommendations[j].Reference)
	})
	sort.Slice(plan.Conflicts, func(i, j int) bool {
		if plan.Conflicts[i].From != plan.Conflicts[j].From { return plan.Conflicts[i].From < plan.Conflicts[j].From }
		return referenceKey(plan.Conflicts[i].Reference) < referenceKey(plan.Conflicts[j].Reference)
	})
	return plan, nil
}

func firstConflict(selected map[string]Node) (string, Reference, bool) {
	ids := make([]string, 0, len(selected))
	for id := range selected { ids = append(ids, id) }
	sort.Strings(ids)
	for _, id := range ids {
		for _, ref := range sortedReferences(selected[id].Relations.Conflicts) {
			target, ok := selected[ref.ID]
			if ok && nodeMatches(target, []requirement{{Ref: ref}}) {
				return id, ref, true
			}
		}
	}
	return "", Reference{}, false
}

func requiredCycle(selected map[string]Node) []string {
	const (unseen = iota; visiting; done)
	stateByID := map[string]int{}
	stack := []string{}
	var visit func(string) []string
	visit = func(id string) []string {
		stateByID[id] = visiting
		stack = append(stack, id)
		deps := selectedRequiredIDs(selected[id], selected)
		for _, dep := range deps {
			switch stateByID[dep] {
			case unseen:
				if cycle := visit(dep); len(cycle) > 0 { return cycle }
			case visiting:
				start := 0
				for i := range stack { if stack[i] == dep { start = i; break } }
				cycle := append([]string(nil), stack[start:]...)
				return append(cycle, dep)
			}
		}
		stack = stack[:len(stack)-1]
		stateByID[id] = done
		return nil
	}
	ids := make([]string, 0, len(selected))
	for id := range selected { ids = append(ids, id) }
	sort.Strings(ids)
	for _, id := range ids {
		if stateByID[id] == unseen {
			if cycle := visit(id); len(cycle) > 0 { return cycle }
		}
	}
	return nil
}

func topologicalOrder(selected map[string]Node) []string {
	visited := map[string]bool{}
	out := make([]string, 0, len(selected))
	var visit func(string)
	visit = func(id string) {
		if visited[id] { return }
		visited[id] = true
		for _, dep := range selectedRequiredIDs(selected[id], selected) { visit(dep) }
		out = append(out, id)
	}
	ids := make([]string, 0, len(selected))
	for id := range selected { ids = append(ids, id) }
	sort.Strings(ids)
	for _, id := range ids { visit(id) }
	return out
}

func selectedRequiredIDs(node Node, selected map[string]Node) []string {
	set := map[string]bool{}
	for _, ref := range node.Relations.Requires {
		if _, ok := selected[ref.ID]; ok { set[ref.ID] = true }
	}
	ids := make([]string, 0, len(set))
	for id := range set { ids = append(ids, id) }
	sort.Strings(ids)
	return ids
}

func sortedReferences(in []Reference) []Reference {
	out := append([]Reference(nil), in...)
	sort.Slice(out, func(i, j int) bool { return referenceKey(out[i]) < referenceKey(out[j]) })
	return out
}

func referenceKey(ref Reference) string {
	return ref.ID + "\x00" + ref.RevisionID + "\x00" + ref.Version + "\x00" + string(ref.Kind) + "\x00" + ref.Namespace + "\x00" + ref.Repository
}

func firstNonEmpty(values ...string) string {
	for _, value := range values { if value != "" { return value } }
	return ""
}
