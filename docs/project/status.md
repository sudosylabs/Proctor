# Project status

This document records capability-level implementation state and unresolved
decisions. It is deliberately not an endpoint, store, or test inventory; use
the code and component contracts for that detail.

## Implemented foundation

- Four independent Go modules are connected by the root workspace: reusable
  cache, mail, and VFS modules plus the Proctor server.
- The module-root `server.New` composition graph owns concrete infrastructure,
  `platform.Service`, `app.App`, HTTP, WebSocket, startup, and shutdown.
- Typed deployment configuration, structured logging, health/readiness,
  graceful shutdown, and the shared `testlib` graph are operational.
- PostgreSQL schema management, the root/per-model store architecture, SQL
  conformance suites, and constrained timing, retry, and local-cache layers
  are implemented.
- The versioned HTTP API has explicit authentication classifications,
  transport DTOs, Problem Details, OpenAPI agreement, request limits, and
  cursor pagination.
- Structural academic administration covers institution, academic units,
  programmes, programme levels, academic periods, classes, affiliations,
  organizational membership, and effective-dated student enrollment.
- Identity includes local passwords, sessions and refresh rotation, account
  recovery, personal access tokens, TOTP MFA and recovery codes, administrative
  session management, direct CAS 3, and generic OIDC.
- Authorization uses current scoped role bindings with institution and
  academic-unit inheritance, exact class scope, durable fail-closed decision
  auditing, protected built-in administration, and scoped user visibility.
- Realtime behavior includes authenticated WebSockets, authorized
  subscriptions, bounded local replay, explicit resynchronization, local and
  Memberlist cluster transports, and best-effort cross-node fan-out.

## Architecture migration acceptance

The required architecture migration was accepted on 2026-08-08. At acceptance,
the dependency-debt ledger was empty, the module-root composition and inward
dependency graph were enforced, OpenAPI agreed with runtime routes and errors,
and the hermetic server gate plus independent module checks passed. The
remaining work below is product development or optional tightening, not an
uncompleted architecture migration.

## Open decisions

- Choose and define the next external identity provider after CAS and OIDC:
  SAML/RENATER or LDAP.
- Define account linking, profile ownership, affiliation reconciliation, and
  provider-driven deprovisioning.
- Decide whether roles may bind directly to programme and programme-level
  scopes.
- Define live sitting amendments, sitting eligibility, proctor assignment,
  integrity evidence and appeals, accommodations, and exact retention periods.
- Decide whether cross-node WebSocket reconnection transfers bounded replay
  queues or always performs authoritative HTTP resynchronization.
- Decide whether generated client SDKs belong in this monorepo and which
  desktop languages are required.
- Define the coderunner threat model, isolation boundary, resource limits,
  supported languages, artifact model, and deployment topology.

## Planned product work

- Build the server-owned file-management boundary incrementally through
  generated and custom profile pictures, validated IDE preferences, searchable
  exam resources, and finally revisioned attempt workspaces with
  execution-environment sync.
- Introduce the durable Job foundation with fenced PostgreSQL claims and
  attempt history as part of the profile-picture slice; its first consumers are
  default generation/reconciliation, expired-upload cleanup, and bounded Job
  retention cleanup.

## Optional engineering follow-ups

- Expand the store cache allowlist only from measured need and after the
  documented staleness review.
- Tighten residual broad transport aggregates when a vertical slice provides a
  stable narrower interface.
