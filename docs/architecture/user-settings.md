# User settings

## Scope and vocabulary

A **User Settings Document** is the one portable, User-owned source document
for client presentation preferences. The initial document format is bounded
JSON with comments (JSONC), presented to the client as `settings.json`. Its
exact source is authoritative server data; parsed values, catalogs, effective
values, caches, schemas, and editor state are derived client concerns.

The document may influence only statically registered presentation behavior.
It cannot grant a capability, select a role or composition, weaken an Exam or
security policy, configure a process or path, change network access, or become
authority for identity, authorization, lifecycle, monitoring, integrity, or
evidence. Unknown structurally valid keys remain exact source but are inactive
unless a future packaged client deliberately registers them.

`settings.json` is a virtual editing surface, not a host file, File Entry, VFS
object, Attempt Workspace entry, deployment configuration source, or generic
file-management target. A workspace file named `.vscode/settings.json` or any
other settings-looking file has no application-configuration authority.

Existing User `locale` and `timezone` fields retain their server meaning for
account and server-rendered behavior. The server never reads those values from
the User Settings Document or synchronizes them from client setting keys. A
future client-only display-language setting, if registered, has a deliberately
different meaning.

## Ownership and derived behavior

The server owns:

- the exact source document and its format version, opaque revision, and
  timestamps;
- session-authenticated self access, conditional replacement, and command
  idempotency;
- structural JSONC grammar and resource-limit enforcement;
- atomic creation and persistence, safe mutation audit, and content-free
  post-commit change notification.

The packaged desktop client owns:

- the immutable registry of recognized keys and their types, defaults,
  applicability, migrations, and prohibitions;
- JSON Schema and editing catalogs derived from that registry;
- effective-value resolution under current trusted composition, platform,
  accessibility, and policy constraints;
- protected local caching, explicit-save editing, conflict merging, source
  presentation, and all resulting visual behavior.

This division keeps the server validator stable while the packaged client
evolves. The server does not duplicate an editor registry or accept a schema
supplied by a client, extension, workspace, Exam, or remote source. Client
interpretation never turns bounded stored input into authority.

## Document contract

Format version 1 is UTF-8 JSONC with exactly one top-level object. It permits
line and block comments, trailing commas, ordinary JSON scalar and collection
values, flat dotted setting identifiers, and bracketed language-override
blocks. Object keys are unique at every level. It permits no executable
expression, include, reference, substitution, non-finite number, or trailing
top-level value. Whether a syntactically valid key or language is registered
remains a desktop decision.

The initial limits are:

| Property | Limit |
| --- | ---: |
| Exact UTF-8 source | 256 KiB |
| Nesting depth | 8 |
| Setting paths | 2,048 |
| UTF-8 bytes per key | 256 |
| UTF-8 bytes per string | 16 KiB |
| Elements per collection | 256 |

`format_version` is server metadata, not a document envelope. The canonical
initial source is exactly `{}\n`. The server preserves every accepted source
byte, including comments and insignificant whitespace; it neither normalizes
nor silently migrates the document.

The initial server accepts replacements only for format version 1. If a server
encounters a persisted newer format after rollback or mixed-version operation,
it returns the exact bounded source as read-only with `writable: false` and
rejects replacement. A packaged registry change does not change the document
format version unless the grammar itself changes.

Invalid replacement is all-or-nothing. A public failure may return at most 32
diagnostics containing a closed code, line, and column. It never echoes source
excerpts, comments, keys, or values. The client already owns the submitted
source and maps locations into its editor.

## Application module and Store seams

One focused User Settings application module exposes only two use cases through
the `app.App` facade:

~~~text
ReadOwn(invocation) -> current exact document
ReplaceOwn(invocation, source, format version, expected revision,
           idempotency key) -> replacement result
~~~

Both derive the User identity from the immutable Principal. No caller supplies
an arbitrary target User ID. The module hides parsing, structural limits,
revision generation, idempotency preparation, audit construction, and effect
ordering. It is not a universal configuration service. A small private parser
belongs with this responsibility rather than in `server/model`, the HTTP
transport, VFS, or the desktop registry.

Persistence exposes a cohesive `UserSettingsStore` for current read and one
named conditional replacement. Replacement atomically checks the expected
revision, records a fresh changed source and opaque revision, commits the
bounded command outcome, and completes mutation audit. It does not expose a
raw transaction callback. User-creation aggregates separately accept the
prepared canonical initial document and insert it in the same transaction as
the User.

The module remains a focused service inside `app` unless a later stable
consumer requires a child package. API and other consumers retain narrow
interfaces; neither receives the root Store or parser mechanics.

## Persistence and lifecycle

PostgreSQL stores one row per User, keyed by a foreign key to that User. The row
contains exact `TEXT` source, positive format version, an opaque randomly
generated revision token, and creation/update timestamps. Database constraints
enforce bounded metadata and the 256-KiB source byte ceiling. JSONB, VFS,
profile revisions, timestamps, and content hashes are not settings-revision
authorities.

Every User-creation path, including bootstrap, local creation, and external
auto-provisioning, inserts the canonical initial settings row atomically. The
pre-release baseline schema supplies the same invariant for existing
development data. A missing row is an integrity failure, not a reason for a
read operation to perform a hidden write.

The server initially retains no current-document history. Disabling or softly
retaining a User retains the settings row under the account lifecycle. Final
hard User deletion cascades to it. Resetting all preferences is an ordinary
conditional replacement with the canonical `{}\n` source; there is no delete
or reset-specific operation.

## Revision, idempotency, and concurrency

The revision is opaque and comparison-only. Any accepted byte change,
including comments or whitespace, creates a new revision and update time. A
byte-identical replacement with the current expected revision succeeds with
`changed: false`, retains the revision and timestamp, and creates neither a
mutation audit record nor a change event. A stale expected revision conflicts
even when submitted source happens to equal the current source.

Replacement requires the normal client-command idempotency key. Its semantic
fingerprint covers the exact source, format version, and expected revision.
Exact replay returns the retained small result and repeats no state change,
audit, or effect; reuse with different semantics is an idempotency conflict.
The retained result contains only revision, format version, update time, and
the changed flag, so it remains below the command-outcome bound. After an
unknown transport outcome, the client performs an authoritative read.

Revision conflicts return a closed `user_settings.revision_conflict` failure
and may include the current revision as a hint, never the current source. The
client reads the current document and performs the accepted three-way merge;
the hint is not a substitute for that read. Last-write-wins replacement,
server-side merge, partial patch, and offline mutation queues are excluded.

## HTTP contract

The initial session-authenticated self resource is:

~~~text
GET /api/v1/users/me/settings
PUT /api/v1/users/me/settings
~~~

Personal Access Tokens, Attempt credentials, system invocations, and
administrative access to another User's document are not admitted. Ordinary
interactive session assurance is sufficient; recent or second-factor
assurance is not required for presentation preferences.

The read response contains:

~~~json
{
  "source": "{}\n",
  "format_version": 1,
  "revision": "opaque-revision",
  "writable": true,
  "updated_at": 0
}
~~~

The replacement body contains `source`, `format_version`, and
`expected_revision`; `Idempotency-Key` is required. A successful response
contains only `revision`, `format_version`, `updated_at`, and `changed` because
the caller already has the submitted source. Both successful reads and
replacements use `Cache-Control: private, no-store`.

The body revision is the sole optimistic-concurrency input. The resource does
not duplicate that contract through `If-Match`; existing content ETags remain
file-transport semantics. There is no PATCH, DELETE, other-User route, list, or
generic file endpoint for settings.

## Audit, events, and privacy

Only a fresh state-changing replacement creates durable mutation audit. Its
safe fields are the User identity, previous and resulting revisions, format
version, source byte count, and server time. Source, comments, keys, values,
source excerpts, and content digests never enter audit records, ordinary logs,
metrics labels, traces, Problem Details, or realtime events.

After a fresh changed commit, the application publishes one best-effort
`user_settings_changed` refetch hint to the User's live sessions. It contains
only revision, format version, and change time. No-op, replayed, and rejected
commands publish nothing. Missed events, reconnection, and foreground
reconciliation use the authoritative HTTP read.

## Deferred and excluded scope

The initial server slice excludes desktop registry implementation,
SettingsWindow UI, effective-value resolution, protected local caches, device
settings, restoration state, multiple profiles, keybindings, import/export,
and server-visible document history.

The Candidate Settings Baseline described by the desktop architecture is also
deferred. This slice creates no Exam Attempt field, baseline row, admission
input, or candidate projection. If that integration is designed later, it
must cross the Attempt's own named admission seam rather than widening the User
Settings module or treating settings as Exam authority.

## Verification

The implementation is verified by:

- parser unit and fuzz coverage for comments, trailing commas, duplicate keys,
  invalid UTF-8, grammar rejection, every limit, and exact-source preservation;
- Store conformance for conditional replacement, no-op behavior, concurrent
  writers, idempotent replay, missing-row integrity failure, and hard-delete
  cascade;
- integration evidence that every User-creation aggregate creates settings
  atomically and that failed creation leaves neither record behind;
- application tests for self ownership, source-safe errors and audit, format
  compatibility, and post-commit effect suppression;
- HTTP/OpenAPI agreement for session-only access, strict DTOs, body limits,
  conflict mapping, no-store responses, and the absence of unsupported methods;
- realtime local, clustered, replay, reconnect, race, and payload-privacy
  coverage; and
- architecture checks proving the module has no VFS, SQL, transport, desktop
  registry, or Exam dependency.
