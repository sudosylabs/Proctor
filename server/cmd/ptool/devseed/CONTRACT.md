# Development seed contract

`devseed` is an outer development client. It constructs fictional scenarios
through the public HTTP API, with Invitation claims read from local Mailpit.
It imports only the standard library and never uses application services,
Store interfaces, SQL, VFS, or a second server composition path. The Cobra
adapter owns command presentation; Make owns development infrastructure.

The finite dataset contains no live candidate simulation, fabricated
participation, Attempts, Submissions, Reviews, or execution hosts. Server
publication, authorization, file processing, account admission, and Jobs remain
authoritative. Settings examples are portable source documents; their keys do
not promise that an unbuilt Desktop registry recognizes them.

## Identity and repeatability

Only HTTP loopback origins with explicit ports are accepted. The client does
not use environment proxies, resolve arbitrary DNS names, or follow redirects.
Canonical server origin, Installation ID, dataset version, profile, and seed
are bound to the journal. The first run requires a pristine Installation. A
private marker in its bootstrap description and the generated administrator
credential resolve an unknown bootstrap outcome; once recorded, Installation
identity remains stable even when its presentation is edited.

The numeric seed controls fictional personal details. IDs, passwords, keys,
and authenticator secrets remain independently generated. Relative schedules
and academic dates are anchored in the journal, not recalculated on resume.
`--refresh-sittings` adds a new explicitly requested generation to a completed
fixture without rescheduling existing Sittings or rewriting exam history.

Custom pictures are locally generated PNG artwork uploaded through the normal
profile-picture API; the server owns normalization and renditions. Other Users
retain Default Profile Pictures. Dedicated disabled Users are admitted normally
and disabled through administration; they never own required fixture work.
Manifest `seeded_disabled` and `seeded_custom_picture` flags describe initial
scenarios, not current state after developer edits. Dataset changes advance the
journal version and require a pristine dataset instead of implicit migration.

## Recovery and local state

The mode-0700 state directory contains a mode-0600 journal, manifest, credential
projection, and advisory lock. A process lock prevents concurrent writers and
is released by the OS after a crash. Atomic replacement synchronizes the new
file before rename and then synchronizes its directory. Symlink state files
and permissive existing files are rejected. The development host must be
macOS or Linux.

Every ordinary fixture mutation saves its request before sending it and saves
the result before advancing. Required-idempotency APIs receive a stable
per-operation key; a retry uses the saved revision and body, including exact
settings source and empty upload bodies. Non-idempotent creates reconcile by
their scoped natural key. If a read cannot establish completion, automatic
retries stop after one hour, comfortably within server outcome retention;
elapsed time never authorizes another blind write. Invitation acceptance reconciles through the
Invitation's accepted User and account login proof. Ambiguous or missing
authority stops the command without broad cleanup or silent adoption.

Picture uploads persist their exact source bytes and original strong ETag before
streaming. An interrupted upload can retry only while that ETag still matches.
A changed picture after an unknown outcome is ambiguous because the server
normalizes PNG into WebP; the client stops for inspection instead of adopting
or overwriting it. Completed uploads are never repeated. Disabled-account
response loss reconciles through the administrative directory, with its explicit
`include_disabled` selection and username/ID cursor; completed
disablement is not reapplied after a developer enables that User.

MFA uses the real setup and activation APIs. The enrollment secret is saved
before activation. A lost activation response is reconciled with enabled
status and the retained secret; one-time recovery codes from a lost response
cannot be recovered by a read. The fixture never silently resets an enabled
factor to obtain new codes. Pending enrollment can restart through the normal
setup operation.

Completed steps are not reapplied. Completed runs read their recorded resources
and fail on missing access or missing records instead of repairing developer
edits. Expired access rotates through the saved refresh credential; an expired
or unusable refresh may require a fresh ordinary CLI Session. Replay does not
promise an absence of authentication/audit bookkeeping. A long run
renews a known fixture credential after a 401 and retries its exact request
once; account disablement or failed login stops it. Local output
projections may be regenerated. Legacy shell-seed state is refused rather than
guessed or imported. Reset remains an explicit development-stack operation.

The journal may contain credentials, one-use Invitation claims, and saved
requests. Neither it nor credentials belong in source control or diagnostic
archives. The manifest contains bounded synthetic identities and API paths;
ordinary output contains only progress and file locations. Response errors
withhold arbitrary server bodies. Mail selection binds the exact recipient,
canonical claim URL, and Invitation creation time.

## Verification

Hermetic tests cover origin rejection, response privacy, state permissions,
concurrent writers, deterministic content, paging boundaries, and unknown
outcomes. The integration-tagged test uses the real server graph, an isolated
PostgreSQL database, actual SMTP to Mailpit, and the real file-content adapter
over a disposable memory VFS. It verifies response-loss recovery, unchanged
replay, developer edits, file reads, distinct exercise trees, normalized/default pictures, disabled login,
scoped denial, MFA, and fresh Sittings.
