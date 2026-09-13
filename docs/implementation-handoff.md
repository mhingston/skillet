# Implementation handoff

The repository currently contains a runnable v1 single-node vertical slice through repository admission, durable packages, hybrid skill retrieval, remote MCP search/materialisation, lock restoration, authentication, audit hooks, lifecycle/feedback evidence, and retrieval evaluation.

The shipped service deliberately remains bounded: it does not execute skill scripts or discovered tools, resolve skill dependencies, orchestrate workflows, or modify client harness directories. The optional harness-neutral `skillet-client` performs explicit digest-verified acquisition when invoked by a compatible host.

## vNext roadmap

The accepted vNext direction is tracked by [roadmap issue #30](https://github.com/mhingston/skillet/issues/30). It evolves the same Go product toward separate agent-capability discovery and organisational-knowledge retrieval domains, with governance and evidence primitives, while preserving v1 materialisation/reproducibility guarantees.

[ADR-013](adr/ADR-013-vnext-domain-and-module-boundaries.md) is the architecture contract for that roadmap. It defines the capability, knowledge, governance, evidence, execution, retrieval, scope, progressive-disclosure, and compatibility boundaries. The roadmap is incremental: this handoff does **not** imply that Markdown/OKF knowledge, scoped capabilities, MCP tool metadata discovery, or the other vNext M1 features are implemented yet.

Implementation work should follow the dependency order in #30 and the verification policy established by #32. Scope expansion belongs in a new/revised roadmap issue rather than being folded silently into an implementation slice.

The demonstration repository `mhingston/agent-skills` is an external test corpus only; it must never be copied into this repository.
