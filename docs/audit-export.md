# Audit export

Skillet's SQLite `audit_events` table remains the authoritative audit record. Optional export is deliberately best-effort and transport-neutral: an exporter receives a bounded normalized envelope only after the local audit write or state transaction has committed. Export errors and sink panics are counted and logged, but they never roll back or change catalogue, governance, evidence, or package state.

## Envelope

The export contract is schema version `1` and contains only fixed fields: organization, event type, UTC occurrence time, actor type/id, repository id, skill id, revision id, and request id. Each string field is capped at 256 bytes. Arbitrary `details_json`, raw identity claim maps, queries, transcripts, and bearer credentials are not exported. Values beginning with `Bearer ` are defensively redacted.

## Reference JSONL sink

Export is disabled by default. To enable the deterministic JSON-lines reference sink, set:

```text
SKILLET_AUDIT_EXPORT_SINK=jsonl
SKILLET_AUDIT_EXPORT_TARGET=stdout
```

`SKILLET_AUDIT_EXPORT_TARGET` may instead be a file path. Skillet opens newly created files with mode `0600` and appends one JSON event per line. Providing a target without a sink, omitting a target when enabled, or naming an unsupported sink fails configuration at startup.

The JSONL implementation is only a reference sink. Enterprise SIEM or observability adapters should implement `auditexport.Sink`; no vendor SDK is required by Skillet core.

## Failure semantics

1. Skillet commits the local authoritative audit/state change first.
2. The normalized event is offered to the configured exporter.
3. A sink error or panic increments the exporter's failure counter and invokes the configured failure observer/logging hook.
4. The original successful operation remains successful. Export is not retried by core and does not mutate the local audit record.

This keeps delivery degradation explicit without making an external exporter part of the transaction boundary.
