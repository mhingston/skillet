# ADR-008: Derive skill relationships from catalogue evidence

## Status

Accepted for the initial relationship slice.

## Decision

Skillet will not require skill authors to maintain a first-class relationship ontology such as `alternative_to`, `composes`, or `followed_by`.

The first implementation exposes a bounded set of **semantic neighbours** with each search candidate. Neighbours are derived from cosine similarity between the routing-document embeddings already held by the search index. This adds no relationship-specific embedding calls and does not change retrieval ranking.

Each neighbour is evidence, not workflow semantics. The response names the evidence source (`routing_embedding_cosine`) and returns the similarity score. A consuming agent or harness may use that evidence to discover adjacent capabilities, but must not treat it as proof that skills are alternatives, dependencies, composition steps, or an execution sequence.

Semantic neighbours are:

- limited to active searchable skills allowed by the current search filters;
- isolated to the source skill's organisation when organisation metadata is present;
- omitted when the source or target lacks a stored routing embedding;
- bounded to three neighbours per returned search hit;
- presentation/discovery metadata only and do not contribute to search rank.

## Why

The catalogue already computes and caches routing embeddings for retrieval. Reusing those vectors provides a zero-maintenance, automatically refreshed relationship signal without introducing author-maintained graph metadata or dependency semantics.

This preserves Skillet's boundary as a registry and distribution service. Skill selection and any interpretation of relationships remain with the consuming agent or harness.

## Adapter impact

No materialisation adapter change is required. Existing adapters select and materialise explicit candidate IDs and may safely ignore the additional ranking metadata.

The bundled `find-skills` skill is updated to understand semantic neighbours when it is using Skillet directly through MCP. Host-specific convenience UIs may choose to display the same hints later, but relationship awareness is not required for correct materialisation.

## Follow-on evidence

Issue #4's optional lifecycle telemetry can later add stronger observed signals such as co-use and ordered transitions. Those signals should remain distinguishable from semantic proximity and should be evaluated before influencing retrieval ranking.

Higher-level labels such as `often_used_with`, `followed_by`, `alternative_to`, or `composes` should only be introduced when evidence shows that the distinction is reliable and useful. They should not be inferred from embedding proximity alone.
