# Examination lifecycle reference

## Scope and vocabulary

Proctor is a programming-examination and integrity-review platform. Every exam
attempt has an IDE workspace, but academic grading, scoring, rubrics, and
pass/fail decisions are outside the product boundary. Canonical examination
terms are defined in the repository
[`glossary` skill](../../glossary/SKILL.md).

~~~text
Academic Unit
└── Exam
    ├── one Exam Draft
    ├── immutable Exam Revisions
    ├── Exam Managers
    └── Exam Sittings
        └── Exam Attempts
            ├── sequential Attempt Participations
            │   ├── one bound registered-key Desktop Session
            │   ├── Attempt Connections
            │   └── sequential Browser Activity Sources
            │       └── minimized Browser Activity records
            ├── one immutable Attempt Configuration
            ├── Correction Acknowledgements
            ├── one Attempt Workspace
            ├── Integrity Flags and Evidence
            └── one Submission
                └── one Submission Review
~~~

The planned application boundary is `app/exam`. It owns authoring,
publication, sitting and attempt lifecycle, policy evaluation, integrity
decisions, and effect ordering. Selective `app/exam/resource`,
`app/exam/workspace`, and `app/exam/correction` children own the distinct
resource, workspace, and live-correction mechanics. They may depend inward on
domain types and narrow capabilities but never on the parent application
package, transports, SQL, VFS, or concrete infrastructure. The parent
application remains the public facade. The package comment added with the
first working slice must state this vocabulary, ownership, exclusions, and
dependency direction.

## Authoring, ownership, and publication

An Exam carries stable identity and lifecycle state: its academic unit,
immutable creator, current owner, optional default revision, timestamps,
archive state, and optimistic revision. It does not carry mutable title,
instructions, policy, resources, class, or schedule.

Each Exam has exactly one mutable Draft addressed through the Exam identity.
The Draft contains a required title, optional Markdown instructions, one typed
Exam Policy Set, ordered Exam Resources, an optional Starter Workspace, its
base revision, and optimistic revision. There is no separate summary or
description field. Markdown is bounded UTF-8 stored as authored content;
presentation sanitizes active HTML, scripts, unsafe URLs, and automatic remote
loads without pretending to moderate the teacher's meaning.

Publishing atomically validates the complete Draft, creates an immutable
monotonically numbered Exam Revision, selects it as the future default, rebases
the Draft, records audit and idempotent outcome, and only then emits transient
effects. An unchanged Draft does not produce a redundant revision. A Revision
freezes title, instructions, policy, resource snapshots, Starter Workspace,
publisher, publication time, optional base revision, and
whether it is a standard publication or live correction.

Manager-facing Revision discovery is a separate bounded metadata projection.
It exposes immutable identity, ordering, provenance, digests and aggregate
counts needed to select and compare Revisions, but not instructions, canonical
policy bytes, resource details, Starter Workspace paths, opaque content
identities, or source bytes. Exact authored snapshots remain an internal
application input for later Sitting delivery rather than an HTTP read model.

An open or paused Sitting may receive an urgent instructions, resource, or
Browser Policy
correction without changing identity or forcing students to rejoin. The named
operation creates a live-correction Revision from that Sitting's current
Revision and atomically retargets only the affected Sitting. It records the
old/new revisions, actor, reason, and effective time. Other Sittings remain on
their selected revisions. The operation requires the new and current policy
digests and Starter Workspace digests to match, so
only instructions, Exam Resources, and the separate Browser Policy can change;
eligibility, schedule, Exam Policy, and starter files cannot change
through this path. Selecting the correction as the future default is
explicit, and changing the reusable Draft remains a separate operation.

Resource bytes for a correction are first staged against the exact Sitting,
base Revision, target kind, manager, and expiry. Staging an addition allocates
a new stable resource identity; staging a replacement binds an identity already
present in the base Revision. The final command supplies one complete ordered
manifest: omission removes a resource, order becomes position, an item without
a stage retains its base content, and an item with a ready stage selects those
bytes. Only that atomic final command makes staged bytes visible. It creates no
Draft mutation, selects no future default, and exposes no file, lease, object
key, path, or public-download identity. Exact retries return the original safe
outcome; failed or abandoned stages remain invisible and retention-eligible.

Every live-correction Revision owns an immutable candidate notice: a trimmed
summary of 1 to 500 Unicode characters and at most 2,000 UTF-8 bytes, the
canonically ordered changed areas (`instructions`, `resources`, and/or
`browser_policy`), an explicit sorted `affected_capabilities` selection, and
whether acknowledgement is required. Actual Browser Policy changes require
`browser`; instructions or resources require `submission` and
`workspace`. Authors may select a superset, including when acknowledgement is
not required. Missing, unknown, duplicate, or unsorted selections fail; changing
only the selection cannot manufacture a content correction. One Sitting may
accumulate at most 32 live corrections. The manager's private reason is never
part of the candidate notice. Candidate presentation returns the ordered
notice history and Attempt-owned acknowledgement state instead of relying on
transient delivery.

The creator is immutable provenance and the first Owner and Exam Manager. One
current Owner is protected from removal; ownership transfer is audited and
requires an eligible manager. Ordinary management requires both a current
Exam Manager relationship and the appropriate role permission inherited from
the Exam's Academic Unit. Revoked academic membership or permission denies
access without erasing the relationship. System-administrator override is an
explicit permission path and never manager membership or an exception to
structural invariants.

Manager discovery is a bounded relationship-provenance projection ordered by
grant time and User identity; it never hydrates User profiles. Adding a Manager
and transferring ownership require the target to be an active User with a
current membership in the Exam's exact Academic Unit, checked before the
command and rechecked inside the locked transaction. Relationship, account,
membership, or role loss removes ordinary authority without erasing immutable
grant or creator provenance. Manager addition, removal, and ownership transfer
are revision-fenced, audited, idempotent named operations. Ownership transfer
changes only the owner, the previous owner remains a Manager, and a deferred
database constraint protects the invariant that the owner is always a Manager.
Explicit override bypasses only the actor relationship requirement, never
target eligibility, owner protection, or revision and archive guards.

Each fresh Manager addition or removal atomically records one direct notice to
the affected User. A fresh ownership transfer records two distinct notices:
the new Owner receives their resulting ownership relationship and the previous
Owner is explicitly told that they remain an Exam Manager. These notices freeze
only the safe Exam title, resulting relationship, and action time; they do not
copy other Managers or reveal the actor or authorization detail. Exact command
replay records no duplicate notice, and mail preparation or persistence failure
rolls back the relationship transition.

Exam discovery is a bounded database projection, not an in-memory filter over
unrestricted rows. Ordinary visibility requires the current Manager
relationship, current membership in the Exam's exact Academic Unit, and an
applicable ordinary permission. Explicit override scope is separate and never
manufactures either relationship. The query drives from archive-filtered Exam
rows in descending update-time and Exam-ID index order, applies the complete
keyset cursor and visibility predicates, and stops at the requested limit; its
Draft title and manager count projections do not cause follow-up queries. The
opaque HTTP cursor carries a private version and unsupported versions fail as
invalid requests. Archiving is revision-fenced, audited, and idempotent: one
transaction locks and rechecks the Exam and relationship, records its immutable
archive time and next revision without deleting state, completes audit and the
command outcome, and commits before transient effects. Concurrent new commands
produce one winner and stable stale-or-archived conflicts. Archived Exams
remain available to authorized exact reads but reject later authoring
mutations.

## Resources and starter material

An Exam Resource is read-only supporting material outside the Attempt
Workspace. It uses a stable File Entry and immutable available File Revisions,
with required display name, optional Markdown description, and explicit order.
A display name is trimmed UTF-8 with 1–255 Unicode scalar values; its Markdown
description is at most 16 KiB of UTF-8. Active resources remain in contiguous
zero-based order. Their count and per-resource bytes are governed by the
Institution's Exam Capacity Policy. The default is ten resources of at most
10 MiB each; fixed server safety ceilings are 100 resources and 100 MiB each.
The initial
allowlist is verified PDF, PNG, JPEG, WebP, UTF-8 text, Markdown, CSV, and JSON;
executables, archives, macros, and disk images are excluded. Publication pins
the exact metadata and File Revision. Replacing resource content creates a new
File Revision without breaking published history.

The Starter Workspace is a separate logical hierarchy of initial code and
directories frozen into an Exam Revision. It is copied into a new Attempt
Workspace and is never an Exam Resource or generic File Revision chain. Live
correction cannot alter starter material. A Draft Starter Workspace contains
an Institution-configured maximum entry count, per-file byte limit, and total
file-byte limit. Defaults are 500 entries, 10 MiB per file, and 50 MiB total;
fixed server safety ceilings are 5,000 entries, 100 MiB per file, and 1 GiB
total.
Its current content carries an opaque 26-character URL-safe Workspace Content
Version for optimistic comparison; this token is not an entity identity.
Paths are already-canonical case-sensitive POSIX-relative values with at most
16 segments, 255 UTF-8 bytes per segment, and 1,024 UTF-8 bytes total. Empty,
absolute, dot, dot-dot, repeated or trailing separators, backslashes,
NUL/control characters, and the reserved `.proctor` root are invalid. Empty
directories are metadata. Removing a non-empty directory requires explicit
recursive intent and the current Draft revision. The complete subtree is
archived atomically, the Draft revision advances once, and published or
admitted snapshots retain their immutable content pins.

Candidates receive no download, export, print, public URL, local-folder,
drag-out, or external-open capability for Exam Resources, starter material,
workspaces, or submissions. Protected in-application rendering necessarily
transfers bounded content to the client, so the contract is candidate export
prohibition rather than an impossible claim that bytes never reach the device.
Authorized managers may inspect authored material and sealed submissions in
application; individual Submission and Sitting exports are separately
authorized, audited, retention-aware capabilities. The candidate export
prohibition is unchanged.

Protected HTTP reads are authorization-checked on every request, return inline
content with a strong checksum ETag and `nosniff`, and expose neither storage
paths nor object keys. Exam Resources may be privately cached for five minutes;
mutable Draft Starter Workspace files are private and `no-store`.

The Exam Capacity Policy is one complete five-field Institution policy:
resource count, resource bytes, Workspace entries, Workspace file bytes, and
Workspace total bytes. Updating it is an authorized, audited Institution
mutation. PostgreSQL rechecks the current policy inside each Draft resource or
Starter Workspace mutation, and publication rejects a Draft that exceeds it.
Authorized Exam Draft projections expose the current policy so an Exam Manager
does not need Institution-administration access merely to discover authoring
limits; the projection is advisory and mutation-time PostgreSQL state remains
authoritative.
Lowering a policy does not delete or rewrite existing Draft material: removal,
reordering, and metadata repair remain possible, while new or replacement
content and publication must satisfy the new limits.

Publication freezes the complete policy in the immutable Exam Revision and its
content digest. The frozen Workspace limits govern both Starter Workspace
material and every Attempt Workspace admitted from that Revision. A later
Institution update affects future Draft mutation and publication only; it does
not shrink an open Sitting, an admitted Attempt, a live-correction Revision, or
an immutable Submission. Live correction preserves the base Revision's policy.
Retention belongs to its purpose-specific policy rather than Exam Capacity Policy.

## Sitting lifecycle and eligibility

Each Exam Sitting selects one Exam Revision and exactly one Class whose
Programme belongs to the Exam's exact Academic Unit. Scheduling validates the
active Class lineage, requires a sealed Revision of the same Exam, and requires
the half-open Sitting interval to fit within the Class's Academic Period. A new
or changed schedule must start strictly after PostgreSQL's decision time while
the relevant rows are locked. Authorization never bypasses those structural
rules.

The implemented Sitting slice owns bounded exact and keyset-paginated
discovery plus audited, revision-fenced, idempotent scheduling, rescheduling,
cancellation, and manager lifecycle commands. Only a `Scheduled` Sitting may
be rescheduled or canceled. Each schedule revision atomically queues active-
deduplicated opening and deadline Jobs; superseded Jobs reread PostgreSQL and
finish as harmless no-ops. A permanently deduplicated daily recovery Job runs
at process startup and scans bounded due work so restart or terminal Job
failure cannot strand a Sitting. Opening revalidates the current academic
structure so an administrative lineage or period change after scheduling
cannot admit an ineligible Sitting.

Scheduling, rescheduling, and cancellation also atomically record one bounded
candidate-mail fan-out occurrence, frozen render bundle, and expansion Job.
Expansion pages effective Class membership at the scheduled start and uses a
per-candidate last-communicated projection to coalesce unsent revisions into
scheduled, rescheduled, canceled, or assignment-removed wording. PostgreSQL
relevance locks suppress stale or post-start work before SMTP, while bounded
periodic reconciliation on every node discovers audience changes after an
earlier expansion completed.

~~~text
Scheduled -> Open <-> Paused -> Closing -> Closed
Scheduled -> Canceled
~~~

Durable Jobs open at the scheduled start and enter Closing at
`ScheduledEndAt`. Recovery before the scheduled end opens late without moving
the deadline; recovery after the whole window elapsed cancels with
`schedule_elapsed`. Managers may close early with a reason. Schedule fields may
change before opening; after opening the end may only be extended. Pause blocks
new Attempts, workspace mutation, and submission while retaining
read-only candidate presentation and integrity monitoring. In version 1,
`ScheduledEndAt` is the sole delivery deadline: paused duration does not extend
it and there is no separate effective-deadline field or pause-extension policy.
Manager pause, resume, extension, and early close are exposed as distinct
idempotent HTTP commands with optimistic Sitting revision fences and private
reasons. PostgreSQL time wins deadline races: at or after `ScheduledEndAt`, the
scheduled-end transition owns the reason instead of a competing manager
command. Archiving does not prevent pause or early close from reducing live
capability, while resume and extension remain unavailable after archive.

Closing immediately denies new participation and workspace mutation. Resumable
bounded work seals every unfinished Attempt's last acknowledged workspace,
skips already submitted Attempts, and marks the Sitting Closed only after all
created Attempts are terminal and own sealed Submissions. Entering Closing
atomically queues a non-cancelable sealing Job. It reads Attempt-ID-ordered
pages of at most 100 and reserves at most 1,000 work units per occurrence;
larger populations continue through a permanently deduplicated successor.
Per-Attempt sealing and the following cursor/count checkpoint are separate
durable steps. If a process stops between reservation, domain commit, and
checkpoint, retry conservatively consumes the uncertain reservation and
relies on the Attempt's natural one-Submission invariant before continuing.
The daily lifecycle recovery scan also sees Closing Sittings and recreates
missing or terminally failed sealing work, so transient cluster ownership is
never the completion authority.

Automatic sealing accepts Active or Suspended Attempts, snapshots the same
authoritative Workspace manifest as voluntary submission, retains immutable
VFS object references, and records unresolved Focus Loss uncertainty as
Gapped. It ends only still-active Participation and still-open Connection
records with `sitting_closed`; earlier expiry, policy-suspension, or transport
causes remain intact. Each Attempt transaction completes actorless audit before
commit, and only a fresh commit publishes bounded Submission/Connection facts
and removes the exact live Connection binding. The Sitting remains visibly
Closing when any Attempt is unfinished and closes with database time only when
the bounded completion check finds none. A Sitting with no Attempts closes
through the same check. Manager early-close provenance and the scheduled end
remain unchanged by sealing.

No Attempt or Submission is fabricated for a student who never entered. The
bounded manager no-show view derives candidate identities from Class
membership active at the Sitting's authoritative `OpenedAt`, excludes every
candidate with an Attempt, and pages by opaque candidate identity.

Current membership in the exact Class is checked on every candidate connection.
Missing membership denies that connection without deleting an existing
Attempt or workspace. Restored membership permits a later connection only when
the Attempt and Sitting otherwise allow it. Membership is not continuously
polled during an established connection; an authorized kick is the immediate
manager control.

## Attempts, participation, and enforcement

The first eligible entry lazily creates exactly one ready Exam Attempt under a
unique Sitting/student constraint and copies the frozen Starter Workspace; it
does not fabricate a Participation or Connection. A later secure Desktop
admission atomically activates that ready Attempt and creates the first
Participation and Connection only after candidate, Sitting, registered-key
Session, build compatibility, Attempt Configuration, and runtime posture
checks succeed. The Attempt retains its initial Revision for provenance while
current instructions/resources resolve through the Sitting so an accepted
live correction becomes visible without recreating the Attempt.

The copy is logical copy-on-write bootstrap: PostgreSQL creates one stable
Attempt Workspace plus attempt-owned entry/object metadata that references the
immutable starter bytes frozen in the admission Revision. Admission performs
no VFS copy, and concurrent first connections converge on the same aggregate
before any transient effect. The Store checks exact command replay before
loading or materializing the locked current Revision snapshot, so a lost
response does not depend on stale application preflight data or duplicate
starter identities.

~~~text
Attempt: Ready -> Active -> Suspended -> Ready
         Ready/Active/Suspended -> Submitted

Participation generation 1 -> interrupted/fenced
Participation generation 2 -> ...
~~~

One server-owned Attempt Participation generation and one candidate connection
may be current. PostgreSQL retains every generation under a unique
Attempt/generation identity, its bound registered-key Desktop Session, its
credential hash, lease, start and end times, and closed end reason. An active
Attempt has exactly one current Participation, and that Participation has
exactly one bound Session. A brief recoverable reconnect before lease expiry
may retain the generation and binding, while an expired or interrupted
generation is permanently fenced. Authorized re-allow returns the Attempt to
Ready; fresh authenticated admission and all recovery gates create the next
generation, so an absent candidate receives no unused credential and stale
credentials, renewals, and mutations cannot revive prior access. Individual
Attempt Connections remain durable children of their Participation and every
committed open/close emits a bounded manager notification after commit.

The first secure admission freezes the complete proposed Attempt Configuration
under the current User Settings revision and exact admitted Desktop build,
target, FNV registry identity, and configuration manifest. Its pixel line height
has independent bounds; explicit high-contrast themes, accessibility booleans,
announcement modes and cursor modes are preserved. Approved commands and
keybindings are sorted catalog IDs; every selected keybinding resolves to a
selected Candidate-safe command. Only admitted packaging can register IDs.

The admission transaction creates one opaque configuration revision. SHA-256
covers the complete canonical candidate, including original build, target and
User Settings provenance, excluding only the server revision and digest. Both
candidate and frozen document are bounded at 16 KiB. Reject constraint errors
without clamping. Reconnect, replay and re-allow retain the exact stored object
despite changed account settings. A later verified build can reproduce the same
manifest without rewriting or matching the original build/target provenance.
Unsupported manifests deny activation and leave an existing Ready Attempt Ready.
The candidate renderer receives only effective presentation, approved IDs,
configuration revision and digest; full provenance belongs to the owning
privileged Desktop recovery path and never to manager, audit or generic hints.

Successful connection and candidate presentation return one bounded runtime
capability document derived from the immutable Attempt Configuration, current
Revision, Sitting state, pending correction acknowledgement, and live server
capabilities. It explicitly states whether Workspace mutation, Submission,
and the governed Browser surface are available and why they are
not. The projection is a client instruction, not an authorization grant; each
operation rechecks authoritative state.

WebSocket ping, authenticated Participation renewal, and privileged native
process health are separate facts. Over TLS, a privileged client coordinator
uses one random continuity credential for the generation and sends explicit
authenticated renewals with a monotonic sequence. The server stores only its
hash and acknowledges the generation, accepted sequence, authoritative
database time, and new expiry; duplicate renewals return the accepted outcome
and stale sequences cannot move the lease. The successful connection response
provides the server-owned renewal interval; the external privileged coordinator
schedules explicit renew requests from that contract. Transport ping and
server timers never auto-renew a Participation. The initial runtime targets
renewal every 5 seconds, a 20-second lease, and an expiry scan every 2 seconds.

The client coordinator supplies the same canonical random 32-byte continuity
credential on an exact command retry. Only its SHA-256 digest crosses the Store
boundary or reaches PostgreSQL. A committed command outcome contains a bounded,
hash-free Participation projection; replay rehydrates current Participation and
Connection state and fails closed when the database-time lease has expired or
the Connection has closed.

Candidate HTTP delivery is Session-authenticated and additionally requires the
continuity credential and durable Connection ID in dedicated sensitive headers.
Persistence binds all three selectors to the owning candidate, active unexpired
Participation, open Connection, and readable Sitting state. Presentation reads
resolve current Sitting Revision instructions/resources; the admission Revision
remains provenance, and the Attempt Workspace remains its admission snapshot.
Manager Attempt reads expose bounded lifecycle state but no credential hash or
Session identity.

The candidate's self-scoped `Exam Activity` collection is the authoritative
navigation registry for upcoming, available, in-progress, submitted,
result-available, and past Sittings. It derives only safe allowed actions such
as enter, resume, or view result, includes Submission provenance where
applicable, and never reveals withheld Review state. A manager's separate
Sitting candidate-status board derives presence from the current Participation
lease at one returned server time and distinguishes not-started, ready,
connected, reconnecting, lease-expired, suspended, and submitted candidates.
Both are bounded no-store keyset projections and never expose Sessions,
credentials, Connections, evidence, or private reasons.

PostgreSQL time is authoritative and expiry is exclusive: a lease with
`expires_at <= database_now` can never be renewed. A late renewal invokes the
same named idempotent expiry transition as the recurring runtime scan rather
than waiting for its next pass. The bounded two-second scanner is owned by the
application runtime lifecycle and creates no durable Job occurrence or
permanent deduplication ledger. That operation conditionally claims the
generation so several nodes converge on one result, then atomically ends it with
`lease_expired`, closes the current Connection, creates bounded Connection Loss
evidence and one Integrity Flag, and opens an automatic suspension episode.
Failure to persist the complete audit/evidence/flag/suspension transition leaves
the expired credential unusable, denies new admission, and retries; transient
events are published only after commit.

One failed request or WebSocket ping does not prove Connection Loss. Confirmed
lease expiry does, blocks interaction and offline editing, and always applies
`FlagAndSuspend`; an Exam Manager cannot weaken that invariant. The candidate
sees a safe explanation that secure connectivity could not be renewed and must
ask a manager for re-allow, never internal generation/lease terminology or an
accusation of cheating. Installation-wide outages follow the same fail-closed
rule and produce neutral continuity evidence for manager review.

A manual Kick and an automatic policy Suspension have the same blocking effect
but distinct provenance. Both deny connections, workspace and exam-material
access and student submission. One append-preserving suspension
episode records source, safe candidate reason, private manager reason, linked
flag where applicable, and re-allow decision. Re-allow requires the exact
Suspension, the expected Attempt revision, and a private trimmed UTF-8 reason
of 1 to 1,000 characters. It preserves flags and evidence, closes only the
active episode, resets only the triggering evaluation window, and allows later
violations to enforce again. Concurrent retries return the one idempotent
result or conflict rather than creating several resumptions. Submission or
Sitting closure is terminal; there is no Attempt reopen or resubmission.

## Workspace and submission

Attempt Workspace entries are stable logical file/directory identities with
mutable, normalized, bounded, case-sensitive POSIX-relative paths. Empty
directories exist only as PostgreSQL metadata. VFS/S3 object keys are opaque,
path-independent, and never authorization or hierarchy; renaming therefore
changes metadata without moving an object. Traversal, links, devices, sockets,
and candidate writes beneath `.proctor/` are excluded.

A workspace file has one current mutable content state and an opaque Workspace
Content Version, not one durable File Revision per save. A replacement stages
new opaque bytes, conditionally commits the current pointer/version in
PostgreSQL, acknowledges only that state, and reclaims losing or superseded
objects after unknown outcomes and references are resolved. An ordered
Attempt-scoped journal records identities, paths, mutation keys, resulting
versions, and Workspace Cursors without retaining every prior content body.
Only acknowledged authoritative state may be submitted.

Explicit recursive directory deletion fences the entire live Workspace with
the client's expected Workspace Cursor and removes the root and all
descendants in one transaction. A stale cursor rejects the complete command.
The journal advances once with a recursive deletion record naming the root;
clients remove descendants only across a slash boundary. Ordinary deletion
still requires an empty directory, and ordinary entry mutations do not take
this aggregate cursor precondition. Owned bytes are retired with retained
outcome protection; published or submitted content pins survive.

Every Workspace write rechecks the Open Sitting, active Attempt, current Class
membership, exact active Participation generation and credential, and the
owning Session-bound open Connection. Command idempotency is Attempt-scoped so
an exact retry can recover its retained outcome after reconnect or re-allow;
the current access selector is reauthorized separately and is not part of the
semantic fingerprint. Stable losing stages become reclaimable, while an
outcome-unknown post-ready commit remains fenced until replay or the durable
cleanup safety window resolves reachability.

A required live-correction acknowledgement is owned by the Attempt, not the
Participation, and therefore survives a reconnect or later Participation
generation. Required notices are acknowledged oldest-first through an exact
Revision route with required idempotency and the current Revision,
Participation, generation, bound Session, credential, and open Connection
fence. Each pending acknowledgement blocks only its immutable selected
capabilities. A browser-only notice leaves Workspace and voluntary
Submission authority independent. The sorted pending union is a projection of
the current notices, and each mutation checks its capability inside the existing
aggregate transaction. Pending browser acknowledgements also withhold usable
policy content. The gates do not block protected
reads, lease renewal, focus or Browser Activity delivery, the acknowledgement
itself, or authorized closure. Acknowledgement remains available while the
Sitting is paused. Exact replay repeats current authorization and audit checks
but returns the retained acknowledgement time together with freshly resolved
current Revision and capability state, without repeating the domain mutation.

Normal submission first denies new edits, settles workspace mutations, closes
and reconciles integrity source sequences/gaps, and then atomically creates one
immutable Submission governed by the Sitting's current Revision, marks the
Attempt Submitted, ends Participation, and records audit/idempotent outcome.
The Submission manifest pins the exact acknowledged cursor, logical entries,
paths, current content versions, checksums, media types, and sizes without
duplicating bytes. A crash before the atomic step leaves an interrupted Attempt
rather than inventing a Submission. Automatic Sitting closure seals the last
acknowledged state and records unresolved integrity gaps and missing required
correction acknowledgements without depending on a cooperative client.

Students receive only a safe submission receipt and never browse their sealed
Submission or workspace after terminal state. Authorized managers cannot read
live candidate workspace content, but may inspect the immutable Submission
after sealing.

Voluntary submission rechecks current Class membership and the exact active
Participation generation, credential, Session-bound Connection, expected
current Revision, Workspace Cursor, final Focus Loss sequence, absence of a
pending required correction, and the client's Browser Activity terminal
accounting. Browser Activity is explicitly `not_applicable`, `complete` with a
current source and final sequence, or `gapped` with a bounded reason; a truthful
gap does not block sealing. Earlier incomplete sources remain discrepancy
provenance. The single named atomic Store operation also retains the
idempotent safe receipt; an exact replay repeats current authorization and
audit checks but returns that receipt without repeating realtime or
runtime-unbind effects. Manager inspection authorizes the canonical Submission
resource against the current
Exam Manager relationship and access scope before concealing a mismatched
nested Exam/Sitting/Attempt path. Manifest pagination uses stable Entry identity
only, and protected file reads stream the retained starter- or Attempt-origin
bytes without exposing their storage selectors.

An authorized Exam Manager may end any unfinished Ready, Active, or Suspended
Attempt before the Sitting deadline by supplying the exact Attempt revision, a
private bounded reason, and an idempotency key. The named operation seals the
last acknowledged Workspace state, preserves an already-ended continuity
cause or ends still-live continuity as `manager_ended`, records missing
correction or integrity state as discrepancies, and creates a candidate-safe
Submission with `manager_ended_attempt` provenance. At or after the deadline,
scheduled Sitting closure owns the otherwise equivalent `sitting_closed`
provenance. Candidate, manager, mail, and realtime projections expose the
provenance but never the private reason.

## Policies, integrity, and review

Exam Policy Set is a concrete typed model persisted as one bounded, strictly
decoded JSONB document. Proctor ships reviewed defaults that are
copied into each new Draft; there is no institution-owned default-policy model
or live inheritance. A server upgrade changes only later Draft defaults, never
existing Drafts or Revisions. Teachers customize only supported typed Draft
fields through focused revision-fenced commands, not arbitrary JSON, policy
kinds, expressions, plugins, or executable code.

The document requires `connection_loss`, `focus_loss`, and `native` objects,
with a 64-KiB encoded limit. It has no wire `schema_version` member; the model
keeps its current schema identity internally. Unknown fields,
duplicates, missing fields, invalid combinations, trailing input, and oversized
documents fail closed. Publication decodes and validates the complete typed
value, serializes it canonically, computes its SHA-256 digest, and freezes both
document and digest in the Revision. Exam policy, Browser Policy, and Attempt
Configuration encoding uses the bounded
[`internal/canonicaljson` contract](../../../../server/internal/canonicaljson/README.md):
sorted ASCII keys, exact scalar UTF-8 and JSON.stringify string escapes, and
decimal safe integers. Validate original bytes before typed decoding can erase
duplicates, malformed Unicode, or fractional/exponent integer spelling.
The current pre-release schema changes in place with coordinated clients and
development fixtures, without old-shape readers or compatibility branches.
Unsupported policy shapes deny admission rather than selecting current defaults.

Shipped defaults enable Focus Loss with a two-second minimum, three incidents
within five minutes, and `flag_and_warn`. Connection Loss uses
`flag_and_suspend`. The required native document pins the reviewed registry and
`desktop_candidate` baseline, with every optional family explicitly disabled
and its required disabled fields populated. The complete wire shape is owned by
[Exam Policy Set in OpenAPI](../../../../server/openapi/fragments/examinations/shared.yaml);
the model's default-policy round-trip tests verify the shipped document.

Connection Loss is server-observed from one confirmed lease expiry and its
only valid initial outcome is `FlagAndSuspend`; it has no teacher-editable
incident count, window, or disable switch. Focus Loss is Draft-configurable:
minimum duration is 500 milliseconds to 5 minutes, incident count 1 to 100,
and rolling window 10 seconds to 4 hours and at least the minimum duration.
Its outcome is `Flag`, `FlagAndWarn`, or `FlagAndSuspend`. Disabled Focus Loss
remains explicit with otherwise bounded fields and instructs the client not to
collect or transmit that signal. Receiving one while disabled is an unexpected
bounded diagnostic, not authority to invent an Integrity Flag. Zero, negative,
unlimited, or overflowing values are invalid.

The integrity chain remains explicit:

~~~text
Native Observation
    -> Security Signal
    -> Detector Finding
    -> Integrity Evidence
    -> Integrity Flag
    -> Review Outcome
~~~

Client records are authenticated, versioned, bounded, generation-scoped,
sequenced, idempotent claims with explicit gaps and uncertainty. One Focus Loss
claim carries its Participation generation, monotonic sequence, bounded
duration in milliseconds, and optional bounded source classification. The
server derives identity and receipt time, uses receipt time for the rolling
window, accepts a sequence once, returns the prior result for a duplicate,
rejects stale or fenced-generation claims, and records a gap as uncertainty
without inventing incidents. Client clocks, severity, and guilt are never
authority; Connection Loss remains server-observed.

When the Revision enables a Browser Policy, the trusted Desktop exposes only
one governed Browser surface. Rules are a strict, canonically ordered,
allowlist of network origin, host match, path prefix, redirect policy,
and start rule; ordinary query strings, fragments, credentials, page content,
titles, referrers, headers, cookies, DOM data, and download bodies are never
policy or telemetry fields. Blocked top-level navigation is always recorded
with a closed reason.

The canonical policy contains only `enabled`, and, when enabled, `start_rule_id`
and `rules`. Each rule explicitly selects `record` or `integrity_evidence` and
includes `institution_http_exception`, even when false. HTTP requires that flag
and the same normalized hostname and explicit port as the installation's pinned
HTTPS origin; HTTPS requires false. Authoring, publication and live correction
validate the pin from server configuration. This navigation exception never
permits HTTP credentials or changes the installation's production origin.

The SHA-256 fingerprint includes its `sha256:` prefix and covers only canonical
policy bytes. Candidate projections add the immutable Revision ID and monotonic
Revision number and a separately revisioned Browser Activity disclosure. Disabled
policies still carry that provenance and notice while omitting rules and start
rule. A pending browser correction withholds its policy until acknowledgement.
The notice derives possible integrity evidence from the delivered rules and
refreshes from the current Institution Retention Policy, including preflight
preparation replay; it does not change the policy digest or grant history access.

The current matcher accepts at most 128 rules within 32 KiB; each origin and
path prefix is bounded to 2,048 ASCII bytes. Hosts use bounded ASCII DNS labels
or browser-normalized IPv4, without IDN, punycode, IPv6, trailing dots or wildcard
IP matching. Ports are decimal 1..65535 without leading zeroes; default ports
normalize away. Rule paths reject dot segments (including browser-recognized
encoded dots), malformed escapes and encoded slash/backslash. Authoring removes
one non-root trailing slash and rejects paths requiring other normalization.
Navigation applies browser dot-segment handling while preserving repeated
slashes, unreserved percent escapes and their case. Retained activity validates
that same serialized pathname, rather than treating it as an authored prefix.
Query and fragment never select a rule. The matching order is longest path,
exact host before wildcard, longest configured hostname, then smallest rule ID.
The tracked [URL fixtures](../../../../server/model/testdata/browser_urls.json)
include exact retained components and rejected input forms; their adjacent
JavaScript verifier checks valid serialization against the browser URL API.

Browser Activity is a separate privacy-minimized delivery stream. Each
Participation may own one initial, up to 32 correction and up to 16 runtime-reset
UUIDv4 sources. Separate lifetime allowances are never refunded. A reset
names the exact predecessor and one closed reason; the former source remains
immutable history. Events cover browser open/close, allowed top-level
navigation/redirect, and blocked top-level navigation. They carry a monotonic
sequence, current policy Revision, client time, a reason-minimized location,
and an applicable rule or block reason. Successful navigation and HTTPS policy
failures retain canonical HTTPS host, optional non-default port, and path. A
blocked HTTP navigation retains those canonical network fields with scheme
`http`; other disallowed schemes retain only their lowercase scheme with empty
host and path; and `invalid_url` uses empty scheme, host, and path values.
Query, fragment, credentials, local paths, and scheme payloads are never
retained. Batches contain 1 to 64 events and at most 256 KiB. PostgreSQL
accepts exact replay, rejects changed duplicates and a reorder distance above 4,096, and
acknowledges the highest contiguous and seen sequences plus at most 32 missing
ranges. Delivery is allowed while paused or awaiting correction
acknowledgement so evidence can converge without granting interaction.

Submission closes live Browser sources and exposes a server-owned settlement
inventory spanning every Participation. It does not accept a client completeness
claim or fabricate immutable Focus Loss discrepancies from Browser delivery loss.
Pending takes precedence while any source can still settle. Permanent gaps,
unknown tails and unretained summaries preserve incomplete status. No sources is
`not_applicable`, including when the final policy is disabled. Late accepted
records advance the separate inventory without changing the sealed manifest.
Every Browser detail-exhaustion transition settles all sources in its quota
scope in the same transaction: terminalize missing positions, interpret buffered
events, release pending bytes, refresh Submission settlement and invalidate changed
Review inventory. A refused append or declaration cannot defer this to a later read.
Minimized Browser Integrity Evidence owns both its frozen policy Revision and
SHA-256 policy digest; Review and export retain that provenance after ordinary
Browser Activity is retired.
Each page through the exact Attempt route requires current exact Exam
Manager membership plus the dedicated Browser Activity
view permission. Administrator and general export permissions provide no
history override. History-bearing exports repeat this check on creation,
replay, metadata reads and downloads, including authoritative Store checks.
The response excludes Session, Registration,
Connection, credentials, page content, and private Review state.

A Focus Loss duration equal to or above the configured minimum qualifies. The
threshold fires immediately when the configured count falls within the rolling
window, consumes that evaluation bucket, and begins a fresh count. At most one
open Flag exists per Attempt, policy kind, and Participation generation;
subsequent threshold crossings append evidence to it. `FlagAndWarn` warns the
candidate at most once per generation without requiring acknowledgement and
notifies managers. Re-allow resets only the causal policy window; a later
generation may produce another Flag.

Each policy kind and generation retains at most 100 qualifying evidence
episodes. Overflow retains only a count, first and last receipt times, and
maximum duration. Initial evidence excludes screenshots, webcam, clipboard,
source code, and arbitrary unbounded payloads.

Every submitted Attempt terminates integrity collection as `Settled` or
`Gapped`. One Submission Review may be finalized only after that state is
terminal and every Flag has a `Confirmed`, `Dismissed`, or `Inconclusive`
decision. It contains private manager notes and optional student-facing
Markdown remarks, but no grade, score, rubric, pass/fail, or academic outcome.
Each decision has one current revision, deciding actor and server time, plus a
bounded private rationale. Draft Review changes and decisions advance the one
Review revision so concurrent manager edits cannot silently overwrite each
other.

Finalization is one named, audited, idempotent operation. It locks the sealed
Submission, requires terminal collection and a decision for every current
Flag, caps the combined inventory at 456 Flags, 30,000 evidence rows, and 200 explicit
discrepancies, and freezes their stable identities and decision revisions in a
canonical SHA-256 digest. Browser delivery adds 256 groups and 10,000 copies to
the existing ceilings. The digest also binds the current delivery inventory and
all-source Browser settlement, including count-only overflow. Previous
finalizations remain immutable, revision-keyed integrity snapshots. Newly
accepted late Browser delivery reopens only the current aggregate as a withheld
draft, advances its revision, invalidates its waiver and Sitting records
completion, and marks the affected group decision stale. Managers must record a
new decision before finalization; new events cannot inherit an earlier
disposition. Candidate results are concealed until the new inventory is approved
and released. This transition never changes immutable Submission work.
Release is a separate one-way `submission.release` operation for that finalized
Review revision with its own
current authorization, audit, revision fence, and idempotent outcome. Before
release, the candidate result selector is concealed as not found. After
release it returns only Review, Submission, and Attempt identities, sanitized
approved Markdown remarks, and release time; it never returns evidence,
decisions, private notes/rationales, sealed Workspace content, or an academic
grade.

A versioned Focus Loss record arriving through the exact retained causal
Participation/Connection selector after Submission is not discarded or
reinterpreted as evidence. It enters a bounded append-only `late_focus_loss`
discrepancy stream with PostgreSQL receipt time and monotonic sequence/gap
semantics. Exact replay returns the retained row; changed or stale sequence
conflicts. Finalization and discrepancy insertion serialize on the Submission:
rows committed before finalization enter its frozen inventory, while later
rows remain manager-readable discrepancies and never alter a finalized or
released Review.

## Records completion and preservation

Sitting Records Completion is separate from delivery closure and Review
finalization. A Closed Sitting can be completed only when each remaining
Submission Review is finalized or has an explicit current waiver. A waiver
binds the exact Review revision and accepted discrepancy count, prohibits all
undecided Flags, and excludes the candidate actor. It records private rationale
in its dedicated record and only a closed reason code in ordinary audit.

Fresh accepted late integrity data makes the whole Sitting completion stale.
Edits to a waived draft Review do the same because the acknowledged inventory
has changed. The completion snapshot exposes a revision and an integrity
revision that the manager must acknowledge together. A renewed completion
starts a new retention clock; exact retries and an already-current completion
preserve the original timestamp. Retired integrity categories reject later
ingestion, private Review reads and mutation replay. Renewing completion does
not require recreating already retired Reviews.

Retention Holds address an Exam, Sitting or Submission through exact nested
ownership. They preserve current content and future descendants until manual
release; holding one Submission does not hold its classmates. A hold created
after partial retirement records creation-time retired counts and protects
what remains. An exact wholly retired Submission rejects a new hold. Creation
and retirement serialize on the same Exam/Sitting/Submission fences, so a hold
cannot silently promise to recover a competing retirement commit.

Completion, waiver and hold mutations require idempotency, current credential
and scope rechecks, and successful critical audit in the same transaction.
Every replay rechecks current authority, including retries using the original
audit identity. These operations never themselves enable cleanup or remove
content. The [authorization reference](../../authorization-audit/references/authorization.md#records-completion-and-preservation)
owns their permission and assurance rules.

Examination exports capture an exact explicit work/integrity selection and a
bounded immutable set of sealed Submissions. The dedicated export action
supplements ordinary read authority. Current Exam Manager/exact membership or
scoped override authority and the requester are rechecked for metadata and
download. Self-candidate exports are excluded. A Sitting request is
all-or-nothing at its stated limits, never silently partial. Portable records
select their fields deliberately, retain frozen Review inventory links and
include only requested categories; opaque storage origins remain private.

## Authorization, effects, and persistence

The initial authorization resources are Exam, Exam Sitting, Exam Attempt, and
Submission. Drafts/Revisions authorize through Exam; resources through their
owning Exam/Sitting/Attempt path; flags and reviews through Submission/Attempt.
Closed actions cover creation, view, management, publication, manager changes,
Sitting scheduling/view/management, Attempt control, and Submission
view/review/release.
Student participation is a current relationship-and-state decision rather than
a reusable role permission.

PostgreSQL is authoritative for domain state, logical workspace hierarchy,
current content selection, leases, policy, journals, evidence, reviews,
idempotent outcomes, and retention eligibility. VFS owns only opaque bytes.
Bounded aggregate Store contracts own Exam authoring/publication, Sittings,
Attempts/Participation, Workspace/Submission, and Integrity/Review. Cross-row
transitions use named atomic operations rather than application transaction
callbacks.

All durable state commits before cache invalidation, local-first realtime,
peer fan-out, or transport closure. Manager and candidate subscriptions are
separate and reauthorize current access. Manager events contain safe IDs,
policy kind, outcome, bounded counts, state, revision, time, and evidence
availability. Candidate events contain only their safe state, reason, or
warning. No event contains instructions, resources, workspace paths or
contents, raw policy documents or evidence, credentials, private reasons, or
remarks. Missed events trigger an authoritative refetch.

## Delivery order and deferred scope

Implementation proceeds as complete vertical slices: authoring and managers;
resources/starter material/publication; Sitting delivery and correction;
Attempt admission/Participation; Workspace/Submission; then Integrity/Review.
Each slice includes model, application, named persistence, HTTP/OpenAPI,
authorization, audit, effects, and proportionate unit, race, integration, VFS,
multi-node, and Docker-backed verification.

The initial implemented scope excludes grading, scoring, questions/rubrics,
dedicated proctor assignment,
accommodations, Exam copying or templates, resource search, binary integrity
capture, arbitrary policies, and
offline participation. Exact
close-work budgets remain an explicit decision for their owning slice.
Sitting Records Completion defines the examination retention anchor above.
The revisioned Institution Retention Policy and separate Retention Control
approve bounded cleanup only after preview. The
[file contract](../../files-and-workspaces/references/files.md#institution-retention-policy)
defines category retirement, byte purge, expiring exports and surviving markers. The
pre-release schema extends the single version-1 baseline and requires
development databases to be recreated; it does not add a chain of development
migrations.

Browser evidence groups use Attempt, Participation, frozen Browser Policy
Revision and rule ID. Only verified accepted events with the rule's
`integrity_evidence` outcome qualify. Keep the first 100 eligible minimized
copies within 10,000 records and 8 MiB per Attempt, without rotating prior
copies. Further verified events increment exact overflow count and first/last
server receive times once. At the 256-group limit, one Attempt summary counts
additional verified qualifying events with reason `group_capacity`, without
inventing Flags or a number of distinct omitted groups. Unresolved redirect
provenance and unretained client summaries are uncertainty, never verified
counter input. Integrity copies, groups and prior review snapshots retire with
integrity; ordinary Browser Activity retirement cannot remove them. Lifetime
quota counters are never refunded by either retirement.


Native condition review preserves immutable, catalog-validated occurrence
transitions independently of native operational telemetry, including explicit
unresolved openers and original receipt/interpretation provenance. It requires
current detailed Submission integrity-view authority and no fabricated Flag.
Operational health, permissions, gaps and resets remain uncertainty. The manager
board receives only bounded counts and live source health. A fixed count/digest
binds native evidence into Review finalization; late interpreted transitions
invalidate the current Review/release/waiver without changing sealed work or
prior snapshots. Integrity retirement removes condition copies from both their
review owner and delivery rows and prevents new condition intake. Operational
retirement removes delivery state while preserving separately retained integrity
material. Native evidence rows remain bounded by admitted native record quotas;
SQL physical copies are not additional client receipt or quota allocations.

Current live controls validate all Browser watermark selectors and claimed
contiguous receipts before processing. Owned closed sources and position limits
produce local rejections without invalidating otherwise usable native coverage.
Accepted allocation deltas charge the existing Participation/Attempt counters;
compact replay receipts preserve immutable source ordinals, including in retired
source stubs. Status/receipt/target reads only project deadline changes; audited
lifecycle, declaration, append or expiry operations own persisted interpretation.
