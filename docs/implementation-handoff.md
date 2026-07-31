# Implementation handoff

The repository is implemented incrementally and currently contains a runnable
single-node vertical slice through repository admission, durable packages,
hybrid retrieval, remote MCP search/materialisation, lock restoration,
authentication, audit hooks, and retrieval evaluation. The remaining pilot
work is hardening and end-to-end verification, not a change in product scope.

The service deliberately remains bounded: it does not execute skill scripts,
resolve skill dependencies, modify client harness directories, or install a
companion client binary.

The demonstration repository `mhingston/agent-skills` is an external test corpus only; it must never be copied into this repository.
