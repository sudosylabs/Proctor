# 40 — Make realtime application behavior transport neutral

**Status:** complete  
**Work classification:** Required  
**Blocked by:** #39 Remove legacy application errors and request metadata

**What to build:** Replace application knowledge of WebSocket and cluster wire structures with an application-owned transient-event publisher port.

## Purpose

Replace application knowledge of WebSocket and cluster wire structures with an application-owned transient-event publisher port.

## Background

AP-06 and AP-07 show realtime contracts in model and platform-coupled application publication. This prefactor enables sibling transport extraction.

## Scope

- Define application event intents with stable semantic payloads.
- Inject a narrow publisher and publish only after durable commit.
- Adapt existing WebSocket/cluster delivery behind the port temporarily.

## Files or modules expected to change

server/app realtime publication, model wire contracts, root adapters, focused tests.

## Architectural rules and ADRs

Architecture Realtime and Effects; ADR-0004, ADR-0005, ADR-0018, ADR-0025, ADR-0026.

## Acceptance criteria

- [x] Application code imports no WebSocket or cluster transport contract.
- [x] Durable changes commit before event publication.
- [x] Local-first and loop-free fan-out behavior remains unchanged.

## Validation steps

- `cd server && go test ./app/... -run 'Realtime|Event'`
- `cd server && go test ./...`
- `cd server && go test -race ./...`

## Risks

Envelope or ordering changes can break reconnect behavior. Adapt at the boundary and preserve existing wire tests.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
