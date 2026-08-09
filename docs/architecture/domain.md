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

Exam ownership and targeting remain open product decisions. The canonical
terms and their avoided synonyms live only in [`CONTEXT.md`](../../CONTEXT.md).

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
