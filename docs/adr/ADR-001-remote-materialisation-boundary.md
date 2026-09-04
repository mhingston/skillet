# ADR-001: Remote materialisation boundary

Skillet prepares immutable package acquisition over MCP; it cannot write to a client filesystem. Package bytes stay out of ordinary MCP text and structured results. A compatible agent or the optional harness-neutral client uses its existing network and filesystem capabilities, and the default cache is outside the project repository. Host-specific activation remains outside the service.
