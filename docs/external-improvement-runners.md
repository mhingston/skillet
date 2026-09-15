# External improvement runners (M4.6)

Skillet is the control plane, not the executor. M4.6 defines an opt-in protocol for handing an already-issued M4.1 improvement experiment to an external harness and ingesting authenticated evidence from that harness.

## Enablement

Both flags are required:

```text
SKILLET_IMPROVEMENT_EXPERIMENTS=true
SKILLET_EXTERNAL_IMPROVEMENT_RUNNERS=true
```

If M4.1 experiments are disabled, runner registration and dispatch tools are not exposed even if the runner flag is set.

## Trust boundary

A runner registration is immutable for `(organization, runner_id, version)` and contains only:

- runner identity and version;
- bounded declared capabilities;
- accepted experiment-spec versions;
- an exact capability allow-list plus optional maximum cost/runtime dispatch scope;
- an Ed25519 **public** key.

Runner private keys, repository credentials, bearer tokens, MCP credentials, cloud credentials, and arbitrary tool authority remain outside Skillet. Changed runner configuration requires a new runner version.

Registrations are organisation-scoped and dispatch checks the exact experiment capability, required runner capabilities, accepted experiment-spec version, and configured budget scope before issuing anything.

## Dispatch contract

`dispatch_improvement_experiment` creates canonical data only. It does not start a worker, open a network connection to a runner, invoke a shell, execute a patch, or delegate credentials.

The dispatch uses schema `skillet.external-improvement-runner/v1` and binds:

- deterministic run id and idempotency key;
- exact runner identity/version and registration digest;
- required capabilities;
- exact M4.1 experiment id, spec revision, handoff digest and canonical handoff;
- bounded immutable capability/proposal/evidence references and available SHA-256 digests;
- the experiment budget;
- the signed status/result schemas and authentication mode.

Repeated identical dispatch requests return the same run and dispatch digest. A runner may poll the run record with `get_external_runner_run`; transport-specific callbacks are not required by this protocol version.

## Runner evidence

Non-terminal runner status uses `skillet.external-improvement-runner-status/v1`. Terminal evidence uses `skillet.external-improvement-runner-result/v1`.

Every signed envelope binds all of:

- run id;
- runner id/version and immutable registration digest;
- experiment id;
- exact experiment spec revision;
- exact handoff SHA-256;
- exact dispatch SHA-256;
- monotonically increasing sequence number.

The signature is Ed25519 over canonical Go JSON for the envelope with `signature` set to the empty string. The transmitted signature is standard-base64 encoded.

Status sequences are monotonic. Exact replay of previously accepted evidence is idempotent; a conflicting replay, skipped sequence, bad signature, mismatched runner, spec, handoff, dispatch, or registration digest fails closed.

Runner status is deliberately small: `accepted` and `running`. Terminal result status is one of `succeeded`, `failed`, or `cancelled`.

## Results and policy integrity

A terminal result may contain raw protected measurements, bounded artifact/log references, a failure reason/summary, and resource/cost/runtime/token metadata. All runner-supplied text and references remain untrusted evidence.

The result payload contains **no promotion policy or protected thresholds**. On successful/failed result intake, Skillet passes only raw measurements/evidence to the existing M4.1 experiment store, which evaluates the protected thresholds from the immutable issued experiment. A runner therefore cannot weaken an eval threshold through its result.

Resource usage is retained independently. Skillet derives a budget assessment (`cost_exceeded`, `runtime_exceeded`, input/output token overage) rather than trusting the runner to declare policy compliance.

A cancelled runner result marks the corresponding issued experiment cancelled. Cancellation is control-plane evidence only; Skillet does not claim to stop an already-running external workload.

## Proven adapter

`internal/runnerfixture.DeterministicCI` is the non-training proof adapter. It consumes only an issued dispatch and an external Ed25519 private key, emits signed accepted/running events, and turns a supplied bounded set of deterministic validation measurements into signed terminal evidence.

The fixture intentionally has no dependency on the Skillet HTTP server, database, shell, repository mutation APIs, model runtime, GPU runtime, or workflow engine. The M4.6 E2E test uses it as the external side of the protocol and verifies success, failure, cancellation, tampering, replay, exact binding, budget evidence, and unchanged canonical capability state.

## Explicit non-goals

M4.6 does not add:

- an arbitrary tool broker;
- shell execution inside Skillet;
- MCP credential delegation;
- a generic workflow/state-machine engine;
- an ML/GPU runtime dependency;
- automatic source mutation, merge, activation, ranking, governance or promotion changes;
- a requirement that every harness support this protocol.

The same envelopes can later represent an MLX/autoresearch-style external training run because runner capabilities, immutable inputs, budgets, signed evidence and resource metadata are harness-neutral; M4.6 itself performs no model training.
