# ADR-001: Remote materialisation boundary

Skillet prepares immutable package acquisition over MCP; it cannot write to a client filesystem. Package bytes stay out of ordinary MCP text and structured results. A compatible agent uses its existing shell and filesystem capabilities, and the default cache is outside the project repository. No companion binary is introduced.
