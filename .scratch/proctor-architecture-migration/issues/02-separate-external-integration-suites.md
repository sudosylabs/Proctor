# 02 — Separate external integration suites

**Status:** completed
**Work classification:** Required
**Blocked by:** None — can start immediately

**What to build:** Make unit, PostgreSQL, Redis-compatibility, and external-provider tests explicit so every later ticket has a predictable validation surface.

## Purpose

Make unit, PostgreSQL, Redis-compatibility, and external-provider tests explicit so every later ticket has a predictable validation surface.

## Background

AP-11 notes inconsistent integration-test tagging. Reliable migration gates require deterministic default tests and opt-in infrastructure suites.

## Scope

- Classify existing tests by dependency and execution cost.
- Introduce consistent build tags or documented targets for external services.
- Keep default local tests hermetic and preserve every existing assertion.

## Files or modules expected to change

server test packages, Makefile or equivalent validation entry points, test documentation.

## Architectural rules and ADRs

Architecture Testing and CI; ADR-0009, ADR-0024, ADR-0027.

## Acceptance criteria

- [x] Default tests require no external service.
- [x] PostgreSQL, Redis-compatibility, and provider integration suites have explicit invocations.
- [x] No integration coverage is silently removed.

## Validation steps

- `cd server && go test ./...`
- `Run each documented integration target in its supported environment`
- `Verify test discovery with go test listing commands`

## Risks

Incorrect tags can hide coverage. Compare test counts and package coverage before and after classification.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- All external-service test files use the `integration` build tag; local `httptest` provider-adapter tests remain ordinary tests.
- Mixed SQL-store files retain pure row-conversion tests in the default suite while database conformance entry points moved to a tagged file.
- Default discovery finds 110 tests; discovery with `integration` finds the unchanged pre-migration total of 150 tests.
- The exhaustive `integration-all` entrypoint and dedicated `integration-postgres`, `integration-redis`, `integration-providers`, and `integration-realtime` targets are green; existing conformance target names remain aliases.
- Invoking a tagged suite without its required environment fails explicitly rather than skipping.
