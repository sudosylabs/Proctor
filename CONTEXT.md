# Proctor

Proctor is a self-hosted examination and proctoring platform in which one
logical installation represents one educational institution.

## Installation

**Installation**:
One logical deployment of Proctor representing exactly one institution,
regardless of how many application processes serve it.
_Avoid_: Tenant, university instance

**Institution**:
The university or school represented by an installation.
_Avoid_: Tenant, organization

## Academic structure

**Academic Unit**:
A node in the institution-defined organizational hierarchy, which may contain
child academic units and own programmes.
_Avoid_: Department, faculty, school as universal core types

**Programme**:
A course of study owned by one academic unit, such as a Bachelor of Computer
Science.
_Avoid_: Course, degree

**Programme Level**:
A reusable curriculum stage within a programme, such as Foundation, Year 1, or
Year 2.
_Avoid_: Year, grade as universal core types

**Academic Period**:
An institution-defined enrollment period, such as an academic year.
_Avoid_: School year, semester as universal core types

**Class**:
A concrete student roster for one programme level and academic period.
_Avoid_: Group, cohort

**Class Member**:
A durable, time-bounded record of a student's enrollment in a class.
_Avoid_: Student class, roster entry

**Academic Unit Member**:
A durable record of a user's organizational membership in an academic unit;
membership alone grants no permission.
_Avoid_: Academic role, unit permission

## Examinations

**Exam**:
A reusable authored assessment containing the material and configuration that
may be delivered through one or more exam sittings.
_Avoid_: Exam sitting, exam session

**Exam Revision**:
An immutable published generation of an exam's authored content and delivery
configuration.
_Avoid_: Exam sitting, draft edit

**Exam Sitting**:
A scheduled delivery of an exam to a defined eligible population.
_Avoid_: Exam version, exam session

**Sitting Amendment**:
An auditable correction to student-visible material for a specific sitting
after its exam revision can no longer be changed.
_Avoid_: Silent edit, exam revision

**Exam Attempt**:
One student's private, durable body of work while participating in an exam;
its acknowledged work survives interruption and submission.
_Avoid_: Exam session, exam member

**Exam Instructions**:
The authored problem statement and directions presented to students for an
exam.
_Avoid_: Subject

**Exam Resource**:
A file deliberately made available to students to support understanding or
completion of an exam.
_Avoid_: Subject file, attachment when its exam meaning matters

**Attempt Workspace**:
The isolated collection of working files belonging to one exam attempt.
_Avoid_: Shared workspace, exam folder

**Integrity Flag**:
A recorded indication of suspected examination-rule violation; it is evidence
for review, not a finding of guilt.
_Avoid_: Cheating verdict, automatic violation

**Submission**:
A sealed manifest of the exact attempt-workspace revisions presented for
grading at one submission point.
_Avoid_: Attempt, copied workspace

**Exam Manager**:
The exam creator or a teacher explicitly granted equal authority to manage one
exam; system-administrator override is not membership in this set.
_Avoid_: Proctor, grader

## File content

**File Entry**:
A stable, application-visible file identity with one owner and purpose across
changes to its content.
_Avoid_: FileInfo, blob

**File Revision**:
One immutable generation of a file entry's content and bounded descriptive
metadata.
_Avoid_: File version when referring to an exam version

**File Rendition**:
One stored representation of a file revision, such as a normalized image size;
several renditions do not represent separate content changes.
_Avoid_: File revision, copy

**Workspace Path**:
The location at which a file entry appears within one attempt workspace.
_Avoid_: VFS path, storage key

**Default Profile Picture**:
The system-generated, permanently retained fallback image belonging to one
user when no custom profile picture is active.
_Avoid_: Placeholder URL, shared avatar

## Identity and access

**User**:
A login-capable Proctor account containing profile and account state, but not
credentials, affiliations, roles, or permissions.
_Avoid_: Person, account when referring specifically to the Proctor identity

**Affiliation**:
A time-bounded, non-exclusive relationship between a user and the institution,
such as student, teacher, staff member, or external collaborator.
_Avoid_: User type, role

**External Identity**:
A link between a user and an opaque subject asserted by a configured external
identity provider.
_Avoid_: SSO user, provider account

**Principal**:
The authenticated security identity acting in a request or operation,
including its credential and authentication context.
_Avoid_: User when authentication context matters

**Role**:
A named collection of permitted actions.
_Avoid_: Affiliation, user type

**Role Binding**:
A time-bounded assignment of a role to a user at a defined authorization
scope.
_Avoid_: Membership, permission

**Action**:
A stable domain operation that authorization may permit, such as viewing class
members or managing an academic unit.
_Avoid_: Endpoint, HTTP permission

**Resource**:
The domain object or scope against which an action is authorized.
_Avoid_: Route, record

**Session**:
A revocable server-side authentication context for an interactive client; it
contains no bearer credential or role snapshot.
_Avoid_: Login token, personal access token

**Personal Access Token**:
A finite, revocable credential for API access on behalf of a user, constrained
by explicit action scopes and optionally by academic scope.
_Avoid_: Session, API session

**User Token**:
A purpose-specific, expiring, single-use credential for an account operation
such as email verification or password reset.
_Avoid_: Session token, personal access token
