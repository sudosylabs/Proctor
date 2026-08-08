# Architecture migration acceptance — 2026-08-08

This record closes the required Proctor architecture migration roadmap
(`.scratch/proctor-architecture-migration/issues`, tickets 01–66) against
`docs/proctor-architecture-migration-spec-20260731.md` and the accepted ADRs.

## Decision

**Accepted.** Required migration work is complete. Optional follow-ups are
explicitly out of scope and do not block completion.

## Evidence matrix

| Gate | Command / environment | Result |
| --- | --- | --- |
| Architecture dependency gate (empty debt) | `cd server && make architecture` | pass |
| OpenAPI schema + runtime agreement | `cd server && make openapi-agreement` | pass |
| Server unit tests | `cd server && go test ./...` | pass |
| Server race tests | `cd server && go test -race ./...` | pass |
| Server vet | `cd server && go vet ./...` | pass |
| Independent modules (GOWORK=off) | `packages/{cache,mail,vfs}` and `server`: `go test`, `-race`, `vet` | pass |
| PostgreSQL conformance + app integration | `cd server && make integration-postgres` (compose postgres:16.6) | pass |
| Redis cache integration | `cd server && make integration-redis` (compose redis:7.2) | pass |
| External provider integration | `cd server && make integration-providers` | pass |
| WebSocket + multi-node Memberlist realtime | `cd server && make integration-realtime` | pass |

## Architecture audit summary

- Dependency graph enforced with zero `dependency_debt.txt` entries; forbidden
  production imports fail immediately.
- Module-root `server` package selects concrete cache, mail, VFS, SQL, cluster,
  and external-auth adapters; `platform` owns lifecycle only.
- HTTP uses a consumer-owned `api.Logger` port; WebSocket and cluster transports
  remain sibling packages with narrow ports.
- Application errors are transport-neutral; OpenAPI agrees with runtime routes,
  authentication classifications, DTOs, and stable errors.
- Local and Memberlist cluster modes require no Redis clustering; Redis remains
  optional disposable cache only.
- Domain contracts use entity-specific IDs and native UTC temporal types with a
  pre-release schema baseline.

## Optional follow-ups (non-blocking)

- Cross-node WebSocket replay handoff (still open by product decision).
- External account-linking administration, SAML/LDAP, service accounts.
- Exam-domain vertical slices after business-model confirmation.
- Further store cache allowlist expansion only from measured need.

## Completion criteria

All ticket 66 acceptance criteria are met. No required migration item remains.
