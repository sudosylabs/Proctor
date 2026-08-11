# File management

## Authority and storage

The server owns every file's domain meaning, owner, authorization, lifecycle,
retention, searchable metadata, and relationship to other models. PostgreSQL is
authoritative for that semantic metadata. The reusable VFS owns only streaming
file content and backend-neutral storage operations; VFS paths and listings are
never an authorization or discovery interface.

An acknowledged file change must survive loss of an application node or
execution environment. Shared VFS stores the bytes in clustered production,
while PostgreSQL records the application-visible identity and revision needed
to resolve them. Backend revisions remain opaque storage concurrency tokens
and are not exposed as domain meaning.

The application model separates a stable `File Entry`, its immutable `File
Revision` content generations, and the `Workspace Path` at which an entry is
projected into an attempt workspace. Domain models reference file-entry IDs;
they do not persist VFS `Info`, paths, or backend revisions as domain objects.
This separation lets profile pictures, IDE preferences, exam resources, and
code workspaces share storage mechanics without sharing authorization or
lifecycle policy.

A file revision may have several stored file renditions without pretending
that each representation is a separate content change. Each rendition records
its own dimensions where applicable, media type, byte size, checksum, and
opaque identity. Ordinary files normally have one primary rendition; one
normalized profile-picture revision has 128, 256, and 512 pixel renditions.

Domain-owned relationship records attach file entries to users, exams, and
attempts with enforceable foreign keys. The generic file tables do not use a
polymorphic `owner_type` and `owner_id`; such a pair would weaken referential
integrity and invite authorization by unverified identifiers.

Replacing content creates a revision rather than mutating a prior generation.
The owning use case decides which revision is current and which history must be
retained. This preserves acknowledged attempt work and provides stable inputs
for search, submission, audit, and synchronization.

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

The first profile-picture slice does not introduce a search service. Metadata
and extracted-content search begin with exam resources, where a concrete
authorized search use case exists.

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

## Live attempt workspaces

Each exam attempt has a private, isolated workspace. Shared exam material is a
read-only input; students do not share writable paths or observe one another's
changes. While an attempt is ongoing, only its student may read its working
files through ordinary product access.

Workspace paths are normalized, bounded, case-sensitive POSIX-relative names.
The initial portable contract supports ordinary files and explicit directories
but rejects traversal, symbolic and hard links, devices, and sockets. The
`.proctor/` namespace is reserved for synchronization metadata and other
system-materialized content.

Future execution environments, including Firecracker-backed terminals, are
synchronized projections rather than durable authorities. File replacement is
revision-conditional, conflicts are explicit, and a per-attempt ordered change
journal supports bidirectional synchronization and reconnect recovery. Losing
or disconnecting the execution environment must not discard an acknowledged
change.

Application clients initially stream uploads and downloads through the server.
They see opaque file-entry IDs and purpose-specific routes, never storage keys
or VFS paths. Future short-lived direct-transfer grants require a separate
authorization design; execution environments never receive general VFS
credentials.

Each revision uses a distinct stored object even when its checksum matches
another revision. Checksums prove integrity rather than cross-owner identity;
avoiding cross-domain deduplication keeps deletion, retention, and access
analysis explicit.

The VFS adapter derives a private sharded storage key from each preallocated
file-rendition ID. Domain models and store contracts never carry the resulting
path. This keeps backend layout replaceable while making incomplete uploads
recoverable from their leases.

Replacement requires the expected domain revision. PostgreSQL selects the one
application-visible winner, while VFS conditions provide defense in depth.
Removal atomically makes the entry inaccessible, then schedules unreferenced
content for idempotent physical purge after its recovery window and any legal
hold.

Strict image decoding, bounds enforcement, normalization, and complete
rendition creation are the initial synchronous inspection boundary. Passing
them moves a pending upload to available; malformed content is rejected.
Quarantine remains a real state for future inspection without introducing a
no-op scanner.

Attaching a generated default, setting or replacing a custom picture, and
removing a custom picture increment the User revision. Attaching a persisted
default does not change `ProfilePictureChangedAt` when its rendered fallback
was already identical. A removed custom entry is archived; a later upload
creates a new entry rather than resurrecting it.

Network disconnection alone changes no durable attempt state. Suspected
misconduct creates an integrity flag rather than a finding of guilt and may
trigger a separate suspension policy. `Suspend` blocks writes, `Submit` seals
the current workspace revisions, `Reopen` resumes the same retained workspace,
and `Terminate` permanently ends participation. These transitions are
authorized, revision-checked, and audited; reconnecting a client or execution
environment cannot silently reopen an attempt.

Ordinary proctor access during an attempt is limited to integrity signals, not
the student's file content. Grader access begins only after submission or
final termination and the sitting's configured grading-release point. Any
future exceptional live-content access requires a distinct, audited emergency
capability.
