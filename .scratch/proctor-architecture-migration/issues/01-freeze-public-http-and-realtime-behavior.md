# 01 — Freeze public HTTP and realtime behavior

**Status:** completed
**Work classification:** Required
**Blocked by:** None — can start immediately

**What to build:** Create characterization coverage that fixes the observable HTTP, authentication, cookie, CSRF, audit, and realtime behavior that the migration must preserve.

## Purpose

Create characterization coverage that fixes the observable HTTP, authentication, cookie, CSRF, audit, and realtime behavior that the migration must preserve.

## Background

The migration changes composition and ownership across most server packages. AP-11 identifies the lack of a complete safety net, while the compatibility contract requires existing external behavior to remain stable.

## Scope

- Add black-box characterization tests for representative public routes, errors, authentication policies, cookies, CSRF, audit outcomes, and WebSocket behavior.
- Record intentional compatibility assertions without changing production behavior.
- Required work only; redesigns and new features are excluded.

## Files or modules expected to change

server/app/api tests, realtime/WebSocket tests, shared HTTP test fixtures.

## Architectural rules and ADRs

Architecture Testing and CI; Behavior and Compatibility Contract; ADR-0009, ADR-0024, ADR-0027.

## Acceptance criteria

- [x] Representative success and failure paths are frozen at the public boundary.
- [x] Tests cover stable status codes, problem details, headers/cookies, route authentication, and realtime envelopes.
- [x] The repository compiles and all existing tests remain green.

## Validation steps

- `cd server && go test ./app/api/...`
- `cd server && go test ./...`
- `cd server && go test -race ./...`

## Risks

Characterization can accidentally bless a known defect. Mark observations separately and do not alter behavior unless the specification names the change.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- Public HTTP characterization fixes success and Problem Details documents at the mounted-handler seam.
- Existing browser integration coverage now fixes host-only cookie paths/flags and CSRF machine codes.
- Existing WebSocket integration coverage now requires the stable raw envelope keys while permitting additive fields.
- Existing durable authorization-audit characterization remains green.
- Focused tests, full tests, race tests, vet, build, PostgreSQL conformance, and realtime conformance are green.
