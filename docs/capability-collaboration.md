# Capability collaboration

M3.6 adds small, Skillet-owned collaboration state around a stable capability identity. It is deliberately **not** a social ranking system and it does not change capability source, governance, active revisions, materialisation, locks, or semantic relevance.

## Ownership and persistence

Skillet owns two additive SQLite tables:

- `capability_comments`: one bounded discussion thread per `(organization_id, capability_id)`, with actor, timestamp, optional immutable revision context, body digest, and moderation tombstone metadata.
- `capability_watches`: one idempotent watch per `(organization_id, actor_id, capability_id)`, including the immutable revision viewed when the watch was last confirmed and a comment cursor so activity starts after the initial watch.

The source repository remains authoritative for capability content and publisher governance. Collaboration records never write source metadata or the search index.

## Authorization

Collaboration reuses the M2 trusted identity and resource model. Claims authorization can grant:

- `collaboration.read`
- `collaboration.comment`
- `collaboration.watch`
- `collaboration.moderate`

The browser resolves and authorizes the capability before thread or watch state is disclosed. A caller without access to the capability gets the same not-found collaboration surface rather than a thread-existence oracle. Activity items are re-authorized at read time, so losing capability access also removes subsequent disclosure from the feed.

Maintainers named by existing capability governance may moderate their capability. Operators can be given `collaboration.moderate`. Moderation replaces the thread body with a tombstone, retains its digest and immutable revision context, and emits `capability_comment_moderated` audit evidence without copying the removed body into the audit payload.

## Text and mentions

Comment bodies are bounded valid UTF-8 plain text. `html/template` renders them as inert data; raw HTML is not executed. Markdown-looking text is not interpreted in M3.6.

`@mentions` are intentionally omitted. Skillet has trusted authenticated subjects but no trusted identity-discovery directory, and M3 explicitly rules out introducing a parallel user directory or social graph merely to support mentions.

## Watches and activity

Watch/unwatch is deterministic and idempotent. A new watch records the current maximum comment ID for the stable capability, so the in-product Activity view shows subsequent discussion rather than replaying historical thread content. Repeating a watch does not reset that cursor. There are no public follower counts and no external notification channels in M3.6.

## Evidence-first usefulness

The collaboration view does not compute a star rating, reputation score, popularity boost, or universal quality number. For skill revisions it reports factual counters from existing Skillet-owned evidence:

- **Materialisations prepared**: count of `materialisation_prepared` audit events for the exact revision. This means a package acquisition was prepared; it is not activation, completion, or success.
- **Lifecycle completed / failed**: exact revision-bound `skill_completed` and `skill_failed` events.
- **Effective patterns**: `effective_pattern` structured feedback for the exact revision.
- **Workarounds / corrections**: `workaround_required` plus `user_correction` feedback for the exact revision.
- **Open improvement candidates**: the existing deterministic evidence-derivation result when the caller is separately authorized for `evidence.review`.
- Existing owner, maintainers, governance state, and immutable revision provenance remain visible alongside the counters.

If a capability kind does not participate in the persisted skill-evidence contract, the UI says evidence is unavailable instead of manufacturing zero-valued success signals.

## Ranking invariant

Discussion, watch, moderation, and activity state never enter `search.Document`, routing text, lexical/vector features, reranking, or capability governance. The E2E regression captures search order before collaboration mutations and verifies the same ranked identities/revisions afterwards.

## Browser routes

- `GET /ui/catalogue/{revisionID}/collaboration`
- `POST /ui/catalogue/{revisionID}/collaboration/comments`
- `POST /ui/catalogue/{revisionID}/collaboration/watch`
- `POST /ui/catalogue/{revisionID}/collaboration/comments/{commentID}/moderate`
- `GET /ui/activity`

Mutations use the existing identity-bound browser CSRF mechanism and bounded request bodies. No collaboration data is added to normal MCP capability search responses.
