# 06 — Move infrastructure selection to root composition

**Status:** completed
**Work classification:** Required
**Blocked by:** #05 Move runtime lifecycle ownership to package server

**What to build:** Select concrete store, cache, mail, VFS, external-auth, cluster, and transport adapters only at the module-root composition boundary.

## Purpose

Select concrete store, cache, mail, VFS, external-auth, cluster, and transport adapters only at the module-root composition boundary.

## Background

AP-01 and AP-02 show infrastructure is selected in CLI, platform, and application code. ADR-0010 requires one explicit composition site.

## Scope

- Move concrete adapter selection to root composition incrementally.
- Pass narrow constructed dependencies inward.
- Retain existing configured behavior and deployment choices.

## Files or modules expected to change

server root composition, platform construction, store/cache/mail/VFS/external-auth/cluster wiring.

## Architectural rules and ADRs

Architecture Composition and Dependency Rules; ADR-0004, ADR-0008, ADR-0010, ADR-0017.

## Acceptance criteria

- [x] Concrete infrastructure selection is visible at the root.
- [x] Root runtime construction gives `platform.New` explicit constructed capabilities rather than asking it to select adapters from configuration. Narrowing application services away from the retained platform lifecycle owner remains sequenced in #09–#41, with locator removal in #41.
- [x] Existing memory/Redis cache, disabled/SMTP mail, local/S3 VFS, local/Redis cluster, PostgreSQL store, and CAS/OIDC registry choices remain available with unchanged configuration semantics.

## Validation steps

- `cd server && go test ./...`
- `cd server && go test -race ./...`
- `Run dependency gate and configuration construction tests`

## Risks

Construction-order and cleanup regressions are likely. Preserve ownership explicitly and test every configured adapter branch.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `server.New` now calls root-owned runtime construction and root selector functions cover each configured adapter family.
- `platform.New` rejects missing capabilities and performs lifecycle, health, and dynamic-reconfiguration work without choosing a backend.
- `platform.NewLegacy` is the explicit compatibility path for `app.NewServer`; its removal criterion is completion of the CLI and `testlib` migrations in #07 and #08.
- Focused selection tests cover network-free local/default, SMTP, S3, CAS, and OIDC branches. Existing tagged conformance suites continue to cover Redis and PostgreSQL behavior.
