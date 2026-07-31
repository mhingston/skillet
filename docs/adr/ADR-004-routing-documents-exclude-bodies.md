# ADR-004: Routing documents exclude skill bodies

Search initially indexes descriptions, names, selected metadata, compatibility and repository identity. Full skill bodies are excluded to reduce false matches, prompt contamination and context leakage; body indexing can be evaluated later.
