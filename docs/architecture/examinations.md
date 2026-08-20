# Examinations

## Scope and vocabulary

Proctor is a programming-examination and integrity-review platform. Every exam
attempt has an IDE workspace, but academic grading, scoring, rubrics, and
pass/fail decisions are outside the product boundary. Canonical examination
terms are defined in [`CONTEXT.md`](../../CONTEXT.md).

~~~text
Academic Unit
└── Exam
    ├── one Exam Draft
    ├── immutable Exam Revisions
    ├── Exam Managers
    └── Exam Sittings
        └── Exam Attempts
            ├── sequential Attempt Participations
            │   └── Attempt Connections
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
publisher, publication time, optional base revision, and whether it is a
standard publication or live correction.

Manager-facing Revision discovery is a separate bounded metadata projection.
It exposes immutable identity, ordering, provenance, digests and aggregate
counts needed to select and compare Revisions, but not instructions, canonical
policy bytes, resource details, Starter Workspace paths, opaque content
identities, or source bytes. Exact authored snapshots remain an internal
application input for later Sitting delivery rather than an HTTP read model.

An open or paused Sitting may receive an urgent instructions/resource
correction without changing identity or forcing students to rejoin. The named
operation creates a live-correction Revision from that Sitting's current
Revision and atomically retargets only the affected Sitting. It records the
old/new revisions, actor, reason, and effective time. Other Sittings remain on
their selected revisions. The operation requires the new and current policy
digests and Starter Workspace digests to match, so only instructions and Exam
Resources can change; eligibility, schedule, policy, and starter files cannot
change through this path. Selecting the correction as the future default is
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
description is at most 16 KiB of UTF-8. A Draft has at most ten active
resources in contiguous zero-based order, each at most 10 MiB. The initial
allowlist is verified PDF, PNG, JPEG, WebP, UTF-8 text, Markdown, CSV, and JSON;
executables, archives, macros, and disk images are excluded. Publication pins
the exact metadata and File Revision. Replacing resource content creates a new
File Revision without breaking published history.

The Starter Workspace is a separate logical hierarchy of initial code and
directories frozen into an Exam Revision. It is copied into a new Attempt
Workspace and is never an Exam Resource or generic File Revision chain. Live
correction cannot alter starter material. A Draft Starter Workspace contains
at most 500 entries and 50 MiB total file content. Each file is at most 10 MiB.
Its current content carries an opaque 26-character URL-safe Workspace Content
Version for optimistic comparison; this token is not an entity identity.
Paths are already-canonical case-sensitive POSIX-relative values with at most
16 segments, 255 UTF-8 bytes per segment, and 1,024 UTF-8 bytes total. Empty,
absolute, dot, dot-dot, repeated or trailing separators, backslashes,
NUL/control characters, and the reserved `.proctor` root are invalid. Empty
directories are metadata; removing a non-empty directory is rejected rather
than recursive.

Candidates receive no download, export, print, public URL, local-folder,
drag-out, or external-open capability for Exam Resources, starter material,
workspaces, or submissions. Protected in-application rendering necessarily
transfers bounded content to the client, so the contract is candidate export
prohibition rather than an impossible claim that bytes never reach the device.
Authorized managers may inspect authored material and sealed submissions in
application; any future bulk Submission export is a separately authorized,
audited, retention-aware capability.

Protected HTTP reads are authorization-checked on every request, return inline
content with a strong checksum ETag and `nosniff`, and expose neither storage
paths nor object keys. Exam Resources may be privately cached for five minutes;
mutable Draft Starter Workspace files are private and `no-store`.

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
new Attempts, workspace mutation, execution, and submission while retaining
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

The first eligible connection lazily creates exactly one Exam Attempt under a
unique Sitting/student constraint, copies the frozen Starter Workspace, and
records the initial connection atomically. The Attempt retains its initial
Revision for provenance while current instructions/resources resolve through
the Sitting so an accepted live correction becomes visible without recreating
the Attempt.

The copy is logical copy-on-write bootstrap: PostgreSQL creates one stable
Attempt Workspace plus attempt-owned entry/object metadata that references the
immutable starter bytes frozen in the admission Revision. Admission performs
no VFS copy, and concurrent first connections converge on the same aggregate
before any transient effect. The Store checks exact command replay before
loading or materializing the locked current Revision snapshot, so a lost
response does not depend on stale application preflight data or duplicate
starter identities.

~~~text
Attempt: Active <-> Suspended
         Active/Suspended -> Submitted

Participation generation 1 -> interrupted/fenced
Participation generation 2 -> ...
~~~

One server-owned Attempt Participation generation and one candidate connection
may be current. PostgreSQL retains every generation under a unique
Attempt/generation identity, its credential hash, lease, start and end times,
and closed end reason. A brief recoverable reconnect before lease expiry may
retain the generation, while an expired or interrupted generation is
permanently fenced. Authorized re-allow only returns the Attempt to Active;
fresh authenticated admission and all recovery gates create the next
generation, so an absent candidate receives no unused credential and stale
credentials, renewals, and mutations cannot revive prior access. Individual
Attempt Connections remain durable children of their Participation and every
committed open/close emits a bounded manager notification after commit.

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
access, execution, and student submission. One append-preserving suspension
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

Every Workspace write rechecks the Open Sitting, active Attempt, current Class
membership, exact active Participation generation and credential, and the
owning Session-bound open Connection. Command idempotency is Attempt-scoped so
an exact retry can recover its retained outcome after reconnect or re-allow;
the current access selector is reauthorized separately and is not part of the
semantic fingerprint. Stable losing stages become reclaimable, while an
outcome-unknown post-ready commit remains fenced until replay or the durable
cleanup safety window resolves reachability.

Normal submission first denies new edits, settles workspace mutations, closes
and reconciles integrity source sequences/gaps, and then atomically creates one
immutable Submission, marks the Attempt Submitted, ends Participation, and
records audit/idempotent outcome. The Submission manifest pins the exact
acknowledged cursor, logical entries, paths, current content versions,
checksums, media types, and sizes without duplicating bytes. A crash before the
atomic step leaves an interrupted Attempt rather than inventing a Submission.
Automatic Sitting closure seals the last acknowledged state and records
unresolved integrity gaps without depending on a cooperative client.

Students receive only a safe submission receipt and never browse their sealed
Submission or workspace after terminal state. Authorized managers cannot read
live candidate workspace content, but may inspect the immutable Submission
after sealing.

Voluntary submission rechecks current Class membership and the exact active
Participation generation, credential, Session-bound Connection, expected
Workspace Cursor, and final Focus Loss sequence. Its single named atomic Store
operation also retains the idempotent safe receipt; an exact replay returns
that receipt without repeating realtime or runtime-unbind effects. Manager
inspection authorizes the canonical Submission resource against the current
Exam Manager relationship and access scope before concealing a mismatched
nested Exam/Sitting/Attempt path. Manifest pagination uses stable Entry identity
only, and protected file reads stream the retained starter- or Attempt-origin
bytes without exposing their storage selectors.

## Policies, integrity, and review

Exam Policy Set is a concrete typed model persisted as one bounded, versioned,
strictly decoded JSONB document. Proctor ships reviewed defaults that are
copied into each new Draft; there is no institution-owned default-policy model
or live inheritance. A server upgrade changes only later Draft defaults, never
existing Drafts or Revisions. Teachers customize only supported typed Draft
fields through focused revision-fenced commands, not arbitrary JSON, policy
kinds, expressions, plugins, or executable code.

The document has one integer `schema_version`, required `connection_loss` and
`focus_loss` objects, and a 64-KiB encoded limit. Unknown versions, fields,
duplicates, missing fields, invalid combinations, trailing input, and oversized
documents fail closed. Publication decodes and validates the complete typed
value, serializes it canonically, computes its SHA-256 digest, and freezes both
document and digest in the Revision. Old explicit decoders remain while stored
Revisions use them; an unknown version denies Sitting admission rather than
being reinterpreted with current defaults. Go domain names remain unversioned;
version-specific codec names are internal implementation details.

The initial persisted shape and shipped defaults are:

~~~json
{
  "schema_version": 1,
  "connection_loss": {
    "outcome": "flag_and_suspend"
  },
  "focus_loss": {
    "enabled": true,
    "minimum_duration_milliseconds": 2000,
    "incident_count": 3,
    "window_milliseconds": 300000,
    "outcome": "flag_and_warn"
  }
}
~~~

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
terminal output, source code, and arbitrary unbounded payloads.

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
Flag, caps the inventory at 200 Flags, 20,000 evidence rows, and 200 explicit
discrepancies, and freezes their stable identities and decision revisions in a
canonical SHA-256 digest. The finalized Review has no mutable backdoor.
Release is a separate one-way `submission.release` operation with its own
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

The initial scope excludes grading, scoring, questions/rubrics, code execution,
runners/terminals, dedicated proctor assignment, accommodations, Exam copying
or templates, resource search, configurable retention/export/deletion, binary
integrity capture, arbitrary policies, and offline participation. Exact
workspace quotas, close-work budgets, and record retention remain explicit
decisions for their owning slices. The
pre-release schema extends the single version-1 baseline and requires
development databases to be recreated; it does not add a chain of development
migrations.
