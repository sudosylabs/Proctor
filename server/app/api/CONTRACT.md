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

The Exam Revision catalog is ordered by immutable Revision number and identity,
both descending. Its `next_cursor` is a versioned opaque URL-safe token carrying
that tuple; clients return it unchanged, and malformed, trailing, versionless,
or unsupported payloads are invalid requests. Publication uses
`POST /api/v1/exams/{exam_id}/revisions`, requires `Idempotency-Key`, and accepts
only the positive `expected_draft_revision` fence. Collection and exact reads
return bounded publication metadata: identity, number, source Draft revision,
title, policy and content digests, aggregate resource/Starter Workspace counts,
publisher, time, base Revision, and publication kind. They never return
instructions, canonical policy bytes, resource metadata or content identities,
Starter Workspace paths, object identities, or source bytes.

## Exam Sitting schedule

An Exam Sitting delivers one immutable Exam Revision to one exact Class over a
half-open scheduled interval. The manager surface covers pre-open scheduling
and the explicit live lifecycle transitions:

| Method and path | Request | Success |
| --- | --- | --- |
| `POST /api/v1/exams/{exam_id}/sittings` | exact Revision, Class, start, and end | `201` Sitting |
| `GET /api/v1/exams/{exam_id}/sittings` | optional bounded filters and cursor | bounded Sitting page |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}` | none | exact Sitting |
| `PATCH /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}` | expected Sitting revision and at least one non-null schedule field | updated Sitting |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/cancel` | expected Sitting revision and private reason | canceled Sitting |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/pause` | expected Sitting revision and private reason | paused Sitting |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/resume` | expected Sitting revision and private reason | resumed Sitting |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/extend` | expected Sitting revision, later RFC 3339 end, and private reason | extended Sitting |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/close` | expected Sitting revision and private reason | Closing Sitting |

Every route requires an authenticated principal. All seven mutations require
`Idempotency-Key`; their bodies are closed, duplicate-free JSON objects.
Schedule instants are RFC 3339 values and start must precede end. PATCH
distinguishes omission from presence and rejects explicit `null` for Revision,
Class, start, and end.
Each private manager reason must be valid UTF-8, already trimmed, between
1 and 1,000 Unicode scalar values, and at most 4,000 encoded bytes.
Pause, resume, extension, and early close lose to the PostgreSQL deadline fence
at or after the scheduled end. Extension must move the end later and remain
inside the current Class Academic Period. Archived Exams still permit pause
and early close to reduce capability, but reject resume and extension.

The list defaults to 50 items and accepts at most 200. It can filter by one
`class_id`, repeated deduplicated `state` values (at most the six defined
states), and a paired `ends_after`/`starts_before` overlap interval. Results are
ordered by scheduled start then Sitting identity, both descending. Its opaque
Raw URL-safe cursor is versioned and carries that exact tuple; clients return
it unchanged.

Responses expose only the Sitting identity, Exam/Revision/Class identities,
schedule, state, lifecycle times, candidate-safe reason code, and optimistic
revision. Private manager reasons, authorization decisions,
audit provenance, and authored Exam content are never returned. All JSON
responses are `no-store`; there is no delete operation.

## Exam resource and Starter Workspace content

Exam Resource and Starter Workspace operations are purpose-specific authoring
surfaces. Every route requires an authenticated principal and applies current
Exam management authorization. Every mutation requires `Idempotency-Key` and
the current `expected_draft_revision`; JSON bodies are closed objects and
reject unknown fields.

| Method and path | Request | Success |
| --- | --- | --- |
| `GET /api/v1/exams/{exam_id}/draft/resources` | none | complete ordered resource catalog |
| `POST /api/v1/exams/{exam_id}/draft/resources` | metadata-first multipart upload | `201` resource |
| `PATCH /api/v1/exams/{exam_id}/draft/resources/{exam_resource_id}` | strict metadata JSON | resource |
| `PUT /api/v1/exams/{exam_id}/draft/resources/order` | strict complete-order JSON | ordered catalog |
| `PUT /api/v1/exams/{exam_id}/draft/resources/{exam_resource_id}/content` | metadata-first multipart replacement | resource |
| `DELETE /api/v1/exams/{exam_id}/draft/resources/{exam_resource_id}` | strict revision-fence JSON | `204` |
| `GET /api/v1/exams/{exam_id}/draft/resources/{exam_resource_id}/content` | optional `If-None-Match` | protected inline bytes or `304` |
| `GET /api/v1/exams/{exam_id}/draft/starter-workspace` | none | complete manifest |
| `POST /api/v1/exams/{exam_id}/draft/starter-workspace/directories` | strict path JSON | `201` directory |
| `POST /api/v1/exams/{exam_id}/draft/starter-workspace/files` | metadata-first multipart upload | `201` file |
| `PATCH /api/v1/exams/{exam_id}/draft/starter-workspace/entries/{starter_workspace_entry_id}` | strict destination-path JSON | moved entry |
| `PUT /api/v1/exams/{exam_id}/draft/starter-workspace/files/{starter_workspace_entry_id}/content` | metadata-first multipart replacement | file |
| `DELETE /api/v1/exams/{exam_id}/draft/starter-workspace/entries/{starter_workspace_entry_id}` | strict revision-fence JSON | `204` |
| `GET /api/v1/exams/{exam_id}/draft/starter-workspace/files/{starter_workspace_entry_id}/content` | optional `If-None-Match` | protected inline bytes or `304` |

Each multipart body contains exactly two parts in order: a non-file `metadata`
part containing one strict JSON object of at most 32 KiB, followed by a
`content` part. Duplicate metadata fields, trailing JSON, missing or reordered
parts, and additional parts are invalid. `size` and lowercase hexadecimal
`sha256` are required metadata. Starter Workspace replacements additionally
require the exact current `expected_content_version`; a stale version is a
conflict. A Workspace Content Version is an opaque 26-character URL-safe
comparison token matching `[A-Za-z0-9_-]{26}`. It is not an entity ID and
clients return it unchanged. The route body limit is 10 MiB plus 64 KiB of
multipart overhead; the streamed content itself remains limited to 10 MiB.

Protected content responses set a strong checksum ETag,
`X-Content-Type-Options: nosniff`, and no `Content-Disposition` header. Exam
Resources use `Cache-Control: private, max-age=300`; mutable Starter Workspace
files use `Cache-Control: private, no-store`. These operations provide only an
authorized in-application content stream. Metadata never exposes VFS paths,
object keys, or public URLs, and the API defines no download/export operation.

## Live Sitting correction

An Exam Manager corrects one Open or Paused Sitting through a two-step,
purpose-bound surface. Both operations require `Idempotency-Key` and current
Exam/Sitting management authorization.

| Method and path | Request | Success |
| --- | --- | --- |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/correction-resource-stages` | metadata-first multipart upload | `201` ready stage metadata |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/corrections` | strict correction JSON | `201` immutable Revision and retarget result |

Stage metadata names the exact `base_revision_id`, target kind (`addition` or
`replacement`), optional replacement resource identity, media type, explicit
size including zero, and lowercase SHA-256 digest. The multipart shape and
10 MiB plus 64 KiB body limit are identical to Exam Resource authoring. A
successful response contains only the purpose-bound stage and resource
identities, authoritative ready rendition metadata, and expiry. File Entry,
File Revision, rendition, upload-lease, VFS key, path, and URL identities never
cross the transport boundary.

The apply body carries the expected Sitting revision, expected current Exam
Revision, required private manager reason, optional `instructions_markdown`,
and a required complete resource manifest of at most ten items. Omitting
`instructions_markdown` preserves it; a present empty string clears it and
explicit `null` is invalid. Resource omission means removal, array order
becomes position, an item without `stage_id` retains the exact base content,
and an item with `stage_id` selects that ready purpose-bound stage. Resource
and non-empty stage identities are unique. Unknown or duplicate JSON members,
including policy, Starter Workspace, future-default, and schedule fields, are
invalid. The response excludes the private reason, authored content, stages,
and storage identities. This surface adds no content download route; current
authoritative presentation remains a later protected delivery seam.

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
