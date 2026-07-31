# Retrieval evaluation

`retrieval.yaml` is a synthetic routing corpus for the first-stage search
pipeline. It contains only names, descriptions, selected metadata fields and
synthetic vectors. It intentionally does not vendor `mhingston/agent-skills`,
skill bodies, Git history, scripts, or provider output.

The suite has the initial handoff mix:

- 20 direct single-intent cases;
- 10 semantic paraphrase cases;
- 10 negative/no-skill controls;
- 10 complementary multi-skill cases.

Run it from the repository root:

```sh
go run ./cmd/skillet-eval --fixtures evals/retrieval.yaml --report retrieval-report.json
```

The command exits non-zero when a gate fails. The JSON report preserves the
ordered returned IDs for every case and records aggregate metrics:

- single-intent top-1 accuracy (direct and paraphrase cases);
- single-intent recall@3;
- multi-intent recall@5;
- negative false-activation rate.

Negative controls use an explicit RRF evidence cutoff (`0.02` by default).
This is a deterministic activation heuristic for the evaluation fixture, not
a calibrated probability and must not be presented as one.

An earlier report can be supplied to enforce the five-percentage-point
regression allowance:

```sh
go run ./cmd/skillet-eval \
  --fixtures evals/retrieval.yaml \
  --baseline retrieval-report.json
```

The evaluator uses `internal/search.Index` with an exact query-to-vector
lookup, so no network model or external corpus is needed. Provider-backed
retrieval evaluation should be added later as a separate, explicitly
versioned experiment rather than changing this deterministic baseline.
