# File management

## Authority and storage

The server owns every file's domain meaning, owner, authorization, lifecycle,
retention, searchable metadata, and relationship to other models. PostgreSQL is
authoritative for that semantic metadata. The reusable VFS owns only streaming
file content and backend-neutral storage operations; VFS paths and listings are
never an authorization or discovery interface.

The server-owned File Content module concentrates backend-neutral content
mechanics over VFS: bounded validation and transformation, immutable rendition
creation, checksums, private key derivation, exact reads, and idempotent
physical deletion. It is stateless and owns no infrastructure lifecycle. The
application retains authorization, semantic availability, publication,
retention decisions, audit, indexing eligibility, and domain events; the sole
composition root selects the concrete VFS backend, constructs File Content
over that one dependency, and passes its bounded capabilities into the
application. File Content starts no goroutines and never closes VFS; the
platform and composition root retain infrastructure lifecycle ownership.

An acknowledged file change must survive loss of an application node or
execution environment. Shared VFS stores the bytes in clustered production,
while PostgreSQL records the application-visible identity and revision needed
to resolve them. Backend revisions remain opaque storage concurrency tokens
and are not exposed as domain meaning.

The application model separates a stable `File Entry` and its immutable `File
Revision` content generations for purposes such as profile pictures and Exam
Resources. Attempt Workspace code uses a separate stable Workspace Entry,
mutable logical Workspace Path, and opaque current Workspace Content Version;
it does not create a retained File Revision for every save. Neither model
persists VFS `Info`, storage paths, or backend revisions as domain meaning.

A file revision may have several stored file renditions without pretending
that each representation is a separate content change. Each rendition records
its own dimensions where applicable, media type, byte size, checksum, and
opaque identity. Ordinary files normally have one primary rendition; one
normalized profile-picture revision has 128, 256, and 512 pixel renditions.

Domain-owned relationship records attach file entries to users and exams with
enforceable foreign keys. Attempt-owned workspace relationships use their own
typed tables. The generic file tables do not use a
polymorphic `owner_type` and `owner_id`; such a pair would weaken referential
integrity and invite authorization by unverified identifiers.

For immutable-revision purposes, replacing content creates a File Revision
rather than mutating a prior generation. The owning use case decides which
revision is current and which history must be retained. Workspace replacement
instead changes the one current opaque content version through the
Attempt-specific acknowledgement protocol described below.

## Domain ownership and events

The file capability returns a generic committed-change result. The owning
application use case emits domain meaning such as
`UserProfilePictureChanged`, `UserIDEPreferencesChanged`,
`ExamResourceChanged`, or `AttemptFileChanged`. The file capability does not
know every consumer or invent domain-specific event names.

IDE preferences are bounded JSON objects whose setting keys are governed by VS
Code. Proctor validates structural safety and denies settings that would weaken
the exam environment; it does not duplicate the evolving VS Code schema. The
normalized preferences are authoritative, and `settings.json` is their
materialized workspace representation.

Clients use purpose-specific operations rather than a generic endpoint that
accepts arbitrary owners or purposes. Profile-picture, exam-resource, and
attempt-workspace use cases reuse the internal file capability but perform
their own authorization, lifecycle, audit, and event publication.

## Upload and availability

Uploads stream to a new unguessable immutable VFS key while enforcing the
purpose-specific size limit and calculating a server checksum. PostgreSQL then
commits the file revision and its domain relationship before the content is
discoverable. A failed database commit may leave only an invisible object;
bounded orphan cleanup removes it after a safety window. Visible metadata must
never point to a partial upload.

Rendition IDs are allocated before storage and produce cryptographically
unguessable, revision-scoped object keys. Backends with conditional creation
use it as defense in depth. Shared-object backends that cannot provide that
primitive write the unique key directly; PostgreSQL publication remains the
authoritative visibility and concurrency fence. This preserves identical
application behavior without emulating a racy stat-then-write condition.

New revisions pass through `Pending` to `Available`, `Quarantined`, or
`Rejected`. Only available revisions may be downloaded, indexed, distributed,
or projected into an execution environment. Content inspection may initially
be a no-op adapter, but consumers cannot bypass the availability boundary.

Retention is purpose-specific. Replaced profile pictures and older IDE
preferences may expire after bounded recovery windows. Exam-resource revisions
remain while a sitting references them; attempt files, journals, and sealed
submission revisions remain for the applicable examination-record period.
Quarantined and rejected content remains only as long as security review and
audit require.

## Search

Search uses server-maintained metadata and bounded, explicitly extracted
content rather than scanning VFS paths or objects on demand. Authorization is
applied from current semantic ownership before results are returned. Runtime
files, secrets, hidden assessment inputs, and other excluded categories never
enter a general content index.

Indexing eligibility is a domain-controlled policy with `None`, `Metadata`,
and `Content` modes, not a client-controlled `searchable` flag. Each immutable
file revision separately records whether indexing is not required, pending,
ready, or failed. This distinguishes permission to index from processing state
and prevents an infrastructure result from broadening access.

The first profile-picture slice does not introduce a search service. Exam
Resources are also initially limited to an authorized catalog of at most ten
items, so metadata or content search remains deferred until a concrete use case
justifies indexing them.

## Profile pictures

The first file-management slice implements file entries and immutable
revisions through profile-picture upload, retrieval, replacement, and removal.
A user manages their own picture; `user.manage` authorizes administrative
changes, and current user-profile visibility governs reads. There is no public
unauthenticated object URL.

Verified PNG, JPEG, and WebP input is bounded to 5 MiB and 4096 by 4096 source
pixels, decoded to defeat malformed or decompression-bomb input, stripped of
metadata, and re-encoded as non-animated square WebP variants at 128, 256, and
512 pixels. Non-square images use a deterministic center crop and are not
upscaled beyond their decoded source. The original upload is not retained by
default.

Every user also has a generated default profile picture retained indefinitely.
Removing a custom picture immediately returns the user to that default while
the custom file revisions follow their ordinary recovery and purge policy.

This relationship remains part of `User`; there is no separate profile-picture
aggregate. `DefaultProfilePictureFileID` references the permanent generated
entry, optional `CustomProfilePictureFileID` selects an uploaded entry, and
`ProfilePictureChangedAt` records only when the visible result changed. It does
not encode custom/default state through timestamp signs or storage-path
conventions.

Default pictures are abstract geometric images generated from a stable random
per-user seed and contain no initials or other profile data. Creation is
idempotent and reconciled after user creation, so VFS availability cannot block
account provisioning. Reads may render the same deterministic fallback until
the generated entry is attached. Concurrent generators race through a named
conditional store operation; losing upload leases are reclaimed.

Clients access only the authorized current-picture route. The generated
default is an internal fallback rather than a separate public endpoint.

The HTTP surface is purpose-specific:

- `GET /api/v1/users/{user_id}/profile-picture?size=128|256|512` returns the
  selected active rendition;
- `PUT /api/v1/users/{user_id}/profile-picture` streams a supported image; and
- `DELETE /api/v1/users/{user_id}/profile-picture` returns to the default.

Mutations require the current ETag through `If-Match`; stale requests conflict.
The ordinary user-profile DTO exposes only the picture update time and
application URL, not file IDs, checksums, leases, indexing state, or storage
details.

Authorized responses use a strong ETag derived from the active normalized
revision, honor conditional requests, and are privately cacheable. A committed
custom change or removal emits `UserProfilePictureChanged` with only the user
ID, active file-entry ID, safe revision, and event time.

Self-service and administrative picture changes are durably audited and fail
closed; administrative changes use `user.manage`. Background default creation
and physical purge produce bounded operational records rather than pretending
to be user actions.

Default creation is part of the initial slice and covers existing users through
idempotent reconciliation.

Default generation, missing-default reconciliation, and expired-upload cleanup
run through the [durable Job system](./jobs.md), making finite background work
traceable and recoverable across nodes.

## Upload leases

Before streaming content, the application creates a short-lived upload lease
with a preallocated revision ID and unguessable storage keys. The final named
store operation atomically attaches the complete available revision, updates
its domain relationship, records audit, and consumes the lease. Expired leases
give cleanup an exact bounded workload without scanning an entire storage
backend.

An Upload Lease initially expires after one hour and may be renewed only by its
authenticated active upload while progress continues. An expired lease cannot
be finalized even when partial objects still exist. The primary database clock
decides expiry and the renewal horizon; lifecycle timestamps remain monotonic
when the creating application node is modestly ahead of that clock.

Every required normalized variant must exist before a profile-picture revision
becomes available. A partial set remains visible only to lease cleanup and can
never be served as a degraded picture.

After every rendition is staged, a named `SetUserProfilePicture` store
operation creates the file metadata, updates the user's custom-file reference
and picture timestamp, records audit, and consumes the upload lease atomically.
Publication and purge scheduling occur only after commit. Normalized content
identical to the current picture is a no-op: it creates no revision, timestamp,
audit event, or realtime event.

## Exam resources and starter workspaces

Exam Resources are a separately authorized read-only catalog outside an
Attempt Workspace. A resource relationship pins a stable File Entry, one exact
available File Revision, required display name, optional Markdown description,
and order. A display name is trimmed UTF-8 with 1–255 Unicode scalar values and
its Markdown description is limited to 16 KiB of UTF-8. A Draft has at most ten
active resources in contiguous zero-based order, each at most 10 MiB. The
accepted content types are verified PDF, PNG, JPEG, WebP, UTF-8 text, Markdown,
CSV, and JSON. A published Exam Revision retains that exact relationship even
when the Draft later replaces or removes the resource. Candidates have
protected in-application reads only—no public URL, download/export, print,
local-folder, external-open, or drag-out capability.

A Starter Workspace is code material rather than an Exam Resource. Mutable
Draft entries are frozen directly into an Exam Revision as an immutable logical
path/content hierarchy, then copied into new Attempt-owned Workspace Entries.
It does not create a generic File Revision chain. Live instructions/resource
correction cannot change the Starter Workspace of an open Sitting. A Draft has
at most 500 Starter Workspace entries and 50 MiB total file content; one file is
at most 10 MiB. Its current content carries an opaque 26-character URL-safe
Workspace Content Version for optimistic comparison; this token is not an
entity identity. Canonical case-sensitive POSIX-relative paths have at most 16
segments, 255 UTF-8 bytes per segment, and 1,024 UTF-8 bytes total. They reject
absolute and empty paths, dot traversal, repeated/trailing separators,
backslashes, NUL/control characters, and the reserved `.proctor` root. Empty
directories are PostgreSQL metadata and non-empty directory removal is not
recursive.

The protected HTTP content operations return inline content only after current
authorization, with a strong checksum ETag and `nosniff`; they never expose a
VFS path, object key, or public URL. Exam Resources use private five-minute
caching. Mutable Draft Starter Workspace files are private and `no-store`.
Candidate Attempt resource and Workspace content reads add the active
Session-bound continuity credential and durable Connection selector. Both are
`private, no-store`: live corrections may retarget candidate resources, and
Attempt Workspace content is private mutable work. Candidate Workspace
manifests expose logical path, kind, bounded content metadata, and opaque
content version only; starter/Attempt object identities and VFS selectors stay
inside the application/content boundary.

## Live attempt workspaces

Each Exam Attempt has one private, isolated, remotely authoritative workspace.
Students do not share writable paths or observe one another's changes. Exam
Resources remain read-only inputs outside it. A manager cannot inspect live
workspace content; authorized access begins only to the immutable Submission
after the Attempt becomes terminal.

A Workspace Entry is a stable logical file or directory identity. Its mutable
Workspace Path is normalized, bounded, case-sensitive, POSIX-relative, and
unique within the Attempt. Empty directories are PostgreSQL metadata and do not
pretend that S3 or another object backend has directory objects. Traversal,
symbolic and hard links, devices, sockets, and candidate writes beneath
`.proctor/` are excluded. Opaque VFS object keys never encode a Workspace Path,
so rename and subtree move change metadata rather than storage layout.

A workspace file has one current mutable content state. Workspace Content
Version is an opaque optimistic comparison token, not a retained File Revision.
Replacement stages bytes under a new unguessable object, conditionally commits
the authoritative pointer/version in PostgreSQL, and acknowledges only after
that commit. Losing objects remain undiscoverable; superseded objects survive
only through the bounded unknown-outcome/reference window and then become
eligible for idempotent purge.

Each accepted mutation has one idempotency key and explicit expected entry,
path, content version, and destination conditions. An Attempt-scoped ordered
journal records identities, old/new paths, resulting content versions,
mutation keys, and Workspace Cursors without retaining the complete body of
every prior save. Reconnect applies ordered changes after the last acknowledged
cursor or refreshes a complete manifest after a gap. Conflicted, rejected, or
outcome-unknown client work remains protected until acknowledged replacement
or explicit discard.

The public protocol is deliberately asymmetric. Authoritative create,
replace, move/rename, and delete commands are HTTP-only, require the active
Session-bound Attempt credential and Connection headers, explicitly repeat the
current Participation identity and generation, and require an idempotency key.
File creation and replacement use exactly two multipart parts in order: strict
JSON metadata, then content, with a 10 MiB plus 64 KiB envelope. Other commands
use duplicate-free strict JSON. WebSocket publishes only the targeted
`exam_attempt_workspace_changed` refetch hint; it carries no path, content,
object selector, credential, Session, Participation, or generation.

The bounded live contract is 500 entries, 16 path segments, 255 UTF-8 bytes per
segment, 1,024 path bytes, 10 MiB per file, 50 MiB total, 200 items per page,
and a retained 4,096-change journal window. Manifest pagination pins a numeric
Workspace Cursor and exposes only the last Entry identity in its opaque cursor;
paths never enter URLs or access logs. If the pinned manifest advances, or a
journal cursor falls behind retention, the response explicitly requires a
full manifest refresh and returns no partial page.

Future execution environments are synchronized projections rather than durable
authorities. Losing a client, node, or execution environment cannot discard an
acknowledged change. The client exposes the workspace only inside the protected
Exam IDE; recovery storage is encrypted and opaque, and candidate export or
ordinary local-folder access is prohibited. Execution environments never
receive general VFS credentials.

Only acknowledged state at an expected Workspace Cursor may be submitted.
Normal submission settles workspace changes and integrity source watermarks,
then one named operation atomically creates the single immutable Submission,
marks the Attempt terminal, ends Participation, and records audit/idempotent
outcome. The manifest pins the final entry identities, paths, content versions,
checksums, media types, and sizes without copying bytes. Sitting closure seals
the last acknowledged state even when the client cannot cooperate. After
submission no workspace mutation or reopening is possible.

The complete Examination Core lifecycle and candidate-containment contract is
[Examinations](./examinations.md).
