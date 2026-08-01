# 13 — Lock the Academic Unit HTTP contract

**Status:** completed
**Work classification:** Required
**Blocked by:** #12 Migrate Academic Unit update and archive

**What to build:** Make the completed reference slice an authoritative example by documenting its public DTOs and proving runtime agreement.

## Purpose

Make the completed reference slice an authoritative example by documenting its public DTOs and proving runtime agreement.

## Background

AP-05 and AP-11 identify direct domain serialization and missing OpenAPI gates. The first migrated slice should establish the contract pattern before replication.

## Scope

- Add the Academic Unit OpenAPI paths and schemas.
- Add agreement tests for routing, authentication, request/response DTOs, and Problem Details.
- Document the reference slice conventions for later tickets.

## Files or modules expected to change

checked-in OpenAPI document, server/app/api Academic Unit agreement tests, architecture examples.

## Architectural rules and ADRs

Architecture HTTP and Testing; ADR-0013, ADR-0023, ADR-0024, ADR-0027.

## Acceptance criteria

- [x] Academic Unit runtime routes and DTOs agree with checked-in OpenAPI.
- [x] Domain entities are not serialized directly.
- [x] The reference conventions are clear enough to copy conceptually for later slices.

## Validation steps

- `Run OpenAPI validation/agreement target`
- `cd server && go test ./app/api/... -run 'AcademicUnit|OpenAPI|Agreement'`
- `cd server && go test ./...`

## Risks

Documenting legacy accidental fields can freeze them. Include only the characterized public contract.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `server/openapi.json` establishes the reviewed OpenAPI 3.1 contract and documents all seven Academic Unit operations, their principal authentication requirement, request/response schemas, stable application errors, and RFC 9457 responses.
- Academic Unit schemas are closed transport contracts. The checked-in response, create, update, collection, and Problem Details shapes are independent of mutable domain entities.
- The agreement test compares every documented Academic Unit method/path with the registrar's runtime route/auth metadata, including normalization of the runtime ID regex.
- The same test compares OpenAPI property names with the actual HTTP DTO JSON tags, checks request and success response references, and proves every documented application error has both a registered HTTP status and a Problem Details response.
- A route-level regression proves application failures retain `application/problem+json`, `Cache-Control: no-store`, stable code/type/status, and explicitly safe fields.
- `make openapi-agreement` provides a focused local and CI-ready agreement target.
- `server/app/api/CONTRACT.md` records the reference-slice conventions and correct/incorrect examples for later vertical migrations.
