# HTTP contract conventions

[`../../openapi.json`](../../openapi.json) is the reviewed public HTTP contract.
Its coverage began with the migrated Academic Unit reference slice and now
includes Institution, Programme, Programme Level, Academic Period, Class,
Affiliation, Academic Unit Member, Class Member enrollment, Exam authoring,
User profiles, account enablement, administrative Session operations, Role
administration, Role Binding administration, Audit listing, and installation
bootstrap without weakening existing contracts.

Use the Academic Unit slice as the conceptual pattern for later capabilities:

- define request and response DTOs in the owning transport file;
- map DTOs explicitly to application commands and results;
- register every route with one authentication requirement and repeat that
  value in OpenAPI's `x-proctor-auth` extension;
- use closed request and response schemas unless a named extension object is
  intentionally open;
- document stable application error codes in `x-proctor-error-codes`, map each
  code to a declared HTTP response, and return RFC 9457 Problem Details;
- add an agreement test that compares registered route/auth metadata, DTO JSON
  fields, success schemas, and public errors with OpenAPI;
- preserve characterized v1 behavior. Contract changes are additive unless a
  new API version and migration path are introduced.

The HTTP Routing Kernel is the construction and execution boundary for HTTP
resources. Each resource declares only recognized path parameters, a narrow
application capability, typed operations, explicit authentication, and an
allowlist of public application errors. The kernel compiles the complete route
catalog before serving, validates results before applying response effects,
owns ordinary response and Problem Details writing, and fails closed when an
operation returns an undeclared error. Redirects, bounded uploads, and binary
downloads use named protocol-specific results recorded in the manifest rather
than an unrestricted response-writer escape hatch. The WebSocket handshake is
the only raw response exception: it is a named, session-authenticated upgrade
operation whose parameters, request metadata, and pre-upgrade failures remain
kernel-owned. After a successful upgrade, the sibling transport owns the
connection lifecycle. The catalog exposes no mutable router or late-registration
seam.

Catalog completion exposed two existing pre-upgrade outcomes that the prior
OpenAPI entry omitted: invalid origin (403) and unavailable WebSocket service
(503). Their declaration is an additive documentation correction for existing
runtime behavior, not a new failure mode.

## Ownership and extension workflow

`api.New` is the production construction boundary. Its broad `Options` value
exists only at composition: construction projects each application capability
through the exact narrow interface accepted by its resource constructor. A
resource may retain that focused application capability, but never `Options`,
`*API`, a router, `store.Store`, SQL, `platform.Service`, or concrete adapters.

To add or change a cohesive resource family:

1. define the focused application capability and transport DTO mapping beside
   the resource;
2. declare typed paths, one explicit authentication requirement per operation,
   typed ordinary or reviewed protocol results, and the complete public-error
   allowlist;
3. add the resource constructor once to the explicit production catalog;
4. update the checked-in OpenAPI operation and declare independently reviewed
   operation, authentication, error, and ordinary DTO/schema expectations
   through the shared agreement-test module; and
5. keep exceptional protocol and compatibility assertions—including headers,
   binary responses, query parameters, forbidden fields, and legacy response
   shapes—explicit beside the owning resource suite.

The agreement-test module owns portable document loading, runtime-path
normalization, deterministic operation comparison, security, request and
success references, public-error parity, Problem Details, and ordinary
DTO/schema agreement. Runtime routes and OpenAPI never generate the expected
contracts they are checked against.

Package initialization, mutable router access, late registration, arbitrary
path regular expressions, and direct persistence or platform access are not
extension mechanisms. `API.Routes` is a defensive manifest projection for
agreement and diagnostics; callers cannot mutate dispatch through it.

These reviewed v1 shapes are frozen compatibility exceptions, not target
patterns:

- its v1 PATCH DTO uses pointers, so omitted and explicit `null` currently have
  the same meaning. Later slices must use the architecture's `Optional[T]`
  representation when those states differ; do not copy the pointer shape;
- its v1 collection response is a bare JSON array. New collection contracts
  use an object with non-null `items` and, where applicable, `next_cursor`;
- Role PATCH uses pointers, so omitted and explicit `null` leave each mutable
  field unchanged while empty strings or arrays are present and validated.
  Later slices use `Optional[T]` when omission and null have different meaning.

The agreement test records these exceptions so migration cannot silently
change existing clients. It does not make them conventions for new endpoints.

The Exam catalog's `next_cursor` is an opaque URL-safe token whose private
payload includes a cursor version, exact update time, and Exam identity.
Clients must return it unchanged. Malformed cursors, trailing payload, and
unsupported versions are invalid requests; versionless tokens emitted before
the cursor version was added remain accepted during v1. Clients never
construct or inspect the payload.

The Exam Manager catalog follows the same opaque-cursor rule with its own
versioned grant-time and User-identity payload. It is ordered by grant time and
User identity, returns relationship provenance and creator/owner indicators,
and never expands User profiles.

## Idempotent commands

Routes declare `none`, `optional`, or `required` idempotency in the immutable
catalog and repeat non-`none` policy in OpenAPI. Existing v1 operations may add
optional support; making the header required needs a new compatible contract.
The initial optional operations are `POST /api/v1/academic-periods` and
`POST /api/v1/academic-units`. New Exam creation, archive, Draft text editing,
and Draft Focus Loss policy replacement require the header because their
contracts are idempotent from introduction. Adding or removing an Exam Manager
and transferring Exam ownership also require it; every request carries the
expected Exam revision in its strict JSON body, including DELETE.

`Idempotency-Key` is one case-sensitive opaque value of 1–128 characters from
letters, digits, `-`, `.`, `_`, and `~`. Transport rejects malformed values;
the application fingerprints a versioned canonical command, and the named
Store mutation atomically commits the successful application outcome. A
matching replay repeats authentication, authorization, and audit but not the
mutation or post-commit effects. Raw keys, fingerprints, commands, stored
outcomes, and replay state never enter public fields or ordinary telemetry.

Correct transport ownership:

```go
type createAcademicUnitRequest struct {
    Name        string `json:"name"`
    DisplayName string `json:"display_name"`
}

unit, err := academicUnits.CreateAcademicUnit(ctx, invocation, command)
writeJSON(writer, http.StatusCreated, academicUnitResponseFromModel(unit))
```

Incorrect domain serialization and transport policy:

```go
var unit model.AcademicUnit
decodeJSON(writer, request, &unit, "update")
store.AcademicUnit().Update(request.Context(), &unit)
```

The OpenAPI document describes the wire contract only. It does not generate or
dictate domain models, application commands, persistence rows, or handlers.
