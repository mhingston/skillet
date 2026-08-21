# ADR-007: Optional semantic versions

`metadata.version` is an optional SemVer 2.0 declaration supplied by a skill
author. Skillet preserves it as descriptive release identity, while the Git
commit/tree and retained archive SHA-256 remain the immutable installation
identity. Exact versions and ranges resolve to one retained revision; ranges
select the highest stable declaration and never silently fall back. Git tags,
automatic upgrades, compatibility claims, and dependency resolution are out
of scope.
