# Domain

## Academic structure

One installation represents one institution. Several application processes
sharing its state are nodes of that installation, not separate tenants.

~~~text
Institution
└── AcademicUnit (hierarchical)
    └── Programme
        └── ProgrammeLevel
            └── Class ── AcademicPeriod
~~~

`AcademicUnit` is a generic hierarchical node rather than a fixed department,
faculty, or school type. Optional presentation classifications may be
institution-defined; authorization follows hierarchy and scoped roles.

The durable invariants are:

- academic-unit cycles are forbidden;
- a programme belongs to one academic unit;
- a programme level belongs to one programme;
- a class belongs to one programme level and academic period;
- a student has at most one active class membership in an academic period;
- progression and transfer close/replace active enrollment transactionally
  while retaining history;
- a user may have several simultaneous affiliations;
- teachers may hold different role bindings in several academic units; and
- affiliation and membership grant no permission by themselves.

## Examination structure

An `Exam` is reusable authored content and configuration. An `Exam Sitting`
binds that assessment to a schedule and eligible population; an `Exam Attempt`
is one student's private participation. Class assignment and start time belong
to the sitting rather than being mislabeled exam versions.

Copying an exam creates an independent draft. It copies the title,
description, instructions, student resources, starter workspace, future
execution-environment specification, and future grading structure. It resets
ownership and manager access, targeting, schedule, proctors, accommodations,
student exceptions, attempts, integrity flags, submissions, grades, audit
history, and lifecycle state. The copying manager becomes the new creator, and
later changes to the source do not propagate. This explicit copy operation
avoids a template aggregate until centrally maintained reusable templates are
a demonstrated requirement.

The exact publication boundary, sitting eligibility rules, integrity policy,
and grading lifecycle remain open product decisions. Canonical terms and their
avoided synonyms live only in [`CONTEXT.md`](../../CONTEXT.md).

After a sitting opens, student-visible description, instructions, and resources
may be corrected only through a sitting amendment. The amendment preserves the
before and after values, reason, author, and effective time and notifies every
affected active student. Starter workspaces, execution configuration, grading
structure, and eligibility cannot be changed through this mechanism. This
permits urgent corrections without rewriting the exam revision or unrelated
sittings.

The creator is immutable provenance and the first exam manager. Every exam
manager initially has equal authority, including copying and granting or
removing managers. System administrators have exceptional access without
becoming managers. A copied exam records the copier as its creator and sole
initial manager.

An attempt remains the lifecycle aggregate when submitted. Each submission is
an immutable manifest of the exact sealed file revisions, not a duplicate copy
of the workspace. Reopening preserves prior submissions; a later submission
becomes the current grading target while earlier submissions remain history.

Integrity detection, access control, review, and academic consequences are
separate decisions. Approved detector rules may atomically create an integrity
flag and suspend an attempt. Review records `Dismissed`, `Confirmed`, or
`Inconclusive`; sanctions and grading consequences require their own
authorized decisions. Reopening an attempt and resolving its flag remain
distinct recorded transitions even when one UI action requests both.

## Model ownership

`model` is one cohesive, flat, domain-focused package. It owns entities, value
objects, entity-specific IDs, principals, authorization actions/resources,
local invariants, and domain transition failures.

It does not own HTTP status, DTOs, SQL rows, request metadata, WebSocket frames,
cluster messages, Redis structures, SMTP objects, filesystem contracts, or
application error identity.

Models use explicit constructors, named domain transitions, and `Validate()
error` for complete rehydrated state. They do not use persistence lifecycle
methods such as `PreSave`, `PreUpdate`, or `IsValid`, nor global clocks or ID
generators.

Domain and application time is UTC `time.Time`; PostgreSQL uses `timestamptz`;
HTTP uses RFC 3339. Optional lifecycle events use nullable, meaning-specific
fields such as `archived_at`, not integer zero sentinels.

Identifiers retain the opaque, URL-safe, random 26-character z-base-32
representation. Every entity has a distinct validated string-backed type such
as `UserID` or `ClassID`; the zero value is invalid, and IDs encode no ordering,
institution, or domain meaning.

Entity-specific type declarations and their domain documentation remain
handwritten. An explicit build-time catalog generates only their uniform
constructor, parser, validation, string, text, and JSON mechanics. The
deterministic output is checked in and freshness-tested; it does not introduce
a runtime-generic identifier or move persistence and transport concerns into
`model`.

Mutable, conflict-prone aggregates carry explicit revisions. Updates compare
and increment the expected revision; timestamps are not concurrency tokens and
revisions are not added mechanically to immutable or append-only records.

## Responsibility and validation

- transport owns encoding, wire shape, and size limits;
- application owns use-case policy, authorization, multi-aggregate
  coordination, audit, and external-effect orchestration;
- domain constructors and transitions own local invariants; and
- atomic stores and database constraints own cross-row and concurrency
  invariants.

Each boundary translates only the failures it owns. Domain validation performs
no I/O. Adapters validate rehydrated state; invalid persisted data is an
internal integrity failure, not a client validation error.

Models expose an `Auditable` projection only when a deliberately safe, bounded
prior/result state is required. It is not an audit event and never contains
credentials, secrets, exam answers, or unbounded user content.
