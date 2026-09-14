# Claude Code marketplace distribution profile

M3 exposes one host-native distribution profile only: a read-only Claude Code
plugin marketplace snapshot built from capability state that has already passed
Skillet governance, scope, and M2 authorization.

## Verified host contract

This profile was verified on 2026-09-14 against:

- Claude Code **v2.1.270** (released 2026-09-12).
- The current Claude Code marketplace guide:
  <https://code.claude.com/docs/en/plugin-marketplaces>.
- Anthropic's current official marketplace examples, which use
  `.claude-plugin/marketplace.json`, `git-subdir` sources, `strict: false`,
  explicit `skills` paths, and immutable `sha` pins.

The emitted marketplace uses the current schema URL
`https://anthropic.com/claude-code/marketplace.schema.json`.

Claude Code accepts a remote `marketplace.json` URL, but a Skillet distribution
snapshot is authorization-sensitive. The browser therefore exports the manifest
as a local snapshot rather than embedding or delegating a Skillet bearer token.
Save it as:

```text
skillet-marketplace/.claude-plugin/marketplace.json
```

Then add the local marketplace:

```sh
claude plugin marketplace add ./skillet-marketplace
```

The distribution page provides the exact `claude plugin install
<plugin>@<marketplace>` command for every included capability.

## Projection

Each distributable Skillet skill becomes one Claude Code plugin entry:

```json
{
  "name": "review-changes-01234567",
  "source": {
    "source": "git-subdir",
    "url": "https://github.com/example/skills.git",
    "path": "skills/review-changes",
    "sha": "0123456789abcdef0123456789abcdef01234567"
  },
  "strict": false,
  "skills": ["./"]
}
```

The source path is the admitted skill directory itself. This intentionally
avoids exporting a repository parent that could contain sibling skills outside
the authorized snapshot. `sha` is the exact admitted Git commit; Claude Code's
Git-source version resolution therefore remains tied to immutable source
provenance rather than Skillet's optional descriptive SemVer.

Skillet keeps the corresponding stable identity, optional capability SemVer,
revision ID, tree SHA, tar.gz SHA-256, and ZIP SHA-256 alongside the generated
manifest in the human distribution view. The manifest SHA-256 is also exposed
as an HTTP response header and ETag.

## Authorization and governance boundary

Before any manifest names, counts, source paths, or provenance are produced:

1. Skillet validates the requested organisation/namespace/repository scope.
2. M2 authorization must allow `capability.search` for that scope.
3. Each capability is re-authorized with `capability.describe` against its
   authoritative scope.
4. Only approved skill sources with exact retained catalogue provenance are
   projected.

Yanked capabilities are never included in a new-distribution artifact and are
not named in omission warnings. Deprecated capabilities remain explicit,
receive a visible warning, and are never followed to a replacement implicitly.
Repository/namespace restrictions are filtering rules only; distribution does
not affect search or routing rank.

For curated collections, the adapter first runs the existing deterministic M3
composition resolver. The exact locked dependency closure is then projected.
If any required member is unauthorized, yanked, unsupported by the Claude skill
profile, or lacks representable immutable source provenance, the collection
export fails closed rather than producing a partial install set.

## Credential and execution boundary

The exported file contains no Skillet token and no source-repository secret.
Claude Code/Git uses the user's existing Git credential helper when a pinned
private repository is installed. Skillet does not mint, proxy, or delegate Git
credentials.

The adapter emits metadata only. It does **not**:

- execute a skill, hook, command, MCP server, or workflow;
- generate a runtime SDK;
- install or enable a plugin on the user's machine;
- copy capability source into a mutable marketplace repository;
- rewrite a locked revision to a newer one;
- change retrieval/ranking behaviour.

An invalid source entry is isolated from an all-capabilities snapshot. A
collection is stricter: because its dependency closure is an atomic curated
selection, one unrepresentable required member makes that collection export
unavailable.

## Determinism

For a fixed authorized snapshot the marketplace bytes are stable:

- capabilities are sorted by stable identity/revision;
- host plugin names are a normalized display slug plus a stable identity hash;
- the JSON representation uses structs rather than unordered metadata maps;
- no generation timestamp is embedded;
- every source is pinned to an exact Git SHA.

A source revision change therefore changes only the affected source/provenance
projection and the resulting manifest digest. There is no implicit upgrade of a
previously exported local snapshot.
