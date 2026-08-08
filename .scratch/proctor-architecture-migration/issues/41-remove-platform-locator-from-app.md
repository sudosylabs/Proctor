# 41 — Remove the platform locator from application code

**Status:** complete  
**Work classification:** Required  
**Blocked by:** #40 Make realtime application behavior transport neutral

**What to build:** Eliminate app.App and focused service dependence on *platform.Service and infrastructure getter methods.

## Purpose

Eliminate app.App and focused service dependence on *platform.Service and infrastructure getter methods.

## Background

AP-02 is the central hidden-dependency violation. Earlier tickets introduce all required narrow seams, allowing this contraction safely.

## Scope

- Replace remaining platform locator access with constructor-injected consumer-owned interfaces and values.
- Remove application-facing infrastructure getters.
- Narrow exported implementation types where no external caller requires them.

## Files or modules expected to change

server/app constructors and capabilities, platform service exposure, root composition, tests.

## Architectural rules and ADRs

Architecture Interfaces, Composition, Dependency Rules; ADR-0002, ADR-0004, ADR-0009, ADR-0010, ADR-0017.

## Acceptance criteria

- [x] server/app has no production import of server/platform.
- [x] Each capability declares only dependencies it consumes.
- [x] Root composition remains the sole concrete wiring location.

## Validation steps

- `Run architecture dependency gate`
- `rg -n 'platform\.Service|server/platform' server/app`
- `cd server && go test ./...`
- `cd server && go test -race ./...`

## Risks

Hidden getter use may surface late. Let compilation expose all consumers and avoid replacing it with another broad aggregate.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
