# ADR-003: Commit and digest locking

Exact Git commits and package SHA-256 digests are authoritative lock boundaries. Branches and tags are discovery aliases. Historical packages are retained so locked content remains retrievable after source changes.
