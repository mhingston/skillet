# ADR-005: Single-node embedded storage

SQLite, derived local indexes and a filesystem package store are deliberate v1 constraints for a single organisational service. Interfaces will isolate future PostgreSQL, distributed search and object storage adapters.
