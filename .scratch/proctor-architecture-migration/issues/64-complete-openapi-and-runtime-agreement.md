# 64 — Complete OpenAPI and runtime agreement

**Status:** completed
**Work classification:** Required
**Blocked by:** #63 Add the constrained local-cache store module

**What to build:** Finish the checked-in public API contract for every current route and make runtime agreement a mandatory validation gate.

## Purpose

Finish the checked-in public API contract for every current route and make runtime agreement a mandatory validation gate.

## Background

AP-05 and AP-11 remain open until every route uses explicit DTOs and ADR-0024 agreement tests protect the entire surface.

## Scope

- Document every current HTTP path, method, authentication requirement, request/response DTO, error, and security scheme.
- Complete runtime-to-OpenAPI agreement tests and route coverage checks.
- Do not add speculative endpoints or breaking revisions.

## Files or modules expected to change

checked-in OpenAPI document, API registrar/DTOs, agreement tests, validation targets.

## Architectural rules and ADRs

Architecture HTTP and Public Contracts; ADR-0013, ADR-0023, ADR-0024.

## Acceptance criteria

- [x] Every registered route is represented in OpenAPI and every documented operation exists at runtime.
- [x] No handler directly serializes mutable domain models.
- [x] Agreement and schema validation fail on drift.

## Completion evidence

- Expanded the checked-in OpenAPI 3.1 document from the previously partial
  administration surface to all 87 registered HTTP operations, without adding
  routes or changing the public v1 wire contract. Every operation declares its
  runtime authentication class, exact security alternatives, request and
  response schemas, stable error codes, and status responses.
- Added transport-owned health and external-provider response DTOs so handlers
  do not serialize mutable domain models. Existing identity, session, MFA,
  Personal Access Token, academic, authorization, audit, and installation
  handlers were verified to map through transport DTOs.
- Added a bidirectional runtime/OpenAPI agreement test that builds the same
  complete route registrar as production, compares normalized path/method and
  authentication contracts, rejects duplicate or missing operation IDs, and
  checks documented error-code/status mappings.
- Added recursive schema-to-DTO agreement coverage for the completed identity
  and system surface, including JSON omission semantics, scalar formats,
  nested objects, arrays, request bodies, success responses, collection item
  types, and the WebSocket upgrade response. The broader agreement suite also
  exposed and corrected an incomplete nested audit-resource schema.
- Added exact per-operation stable-error contracts for the completed identity
  and system surface. These cover wrapper and application failures, including
  fail-closed audit errors, and require the matching Problem Details status
  response.
- Added standards-complete OpenAPI 3.1 parsing and validation with
  `kin-openapi`, supplemented by Proctor's required operation IDs, explicit
  security, authentication classification, and error-code extensions.
  Mutation tests prove missing operation IDs, unresolved component references,
  malformed schemas, and missing path parameters fail validation.
- Made `openapi-validate` and the complete `openapi-agreement` suite mandatory
  prerequisites of the ordinary server `make check` gate.
- Passed OpenAPI schema validation, the route/OpenAPI agreement target,
  `go test ./app/api/...`, and the full server test suite.

## Validation steps

- `Run OpenAPI schema validation`
- `Run route/OpenAPI agreement target`
- `cd server && go test ./app/api/...`
- `cd server && go test ./...`

## Risks

A large final documentation sweep can mask mismatches. Resolve drift capability by capability and avoid redesign.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
