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
An enrollment period owned by the institution or one academic unit and
applicable throughout that owner's academic-unit subtree, such as an academic
year or semester.
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
A reusable assessment identity owned by one academic unit and delivered through
one or more exam sittings.
_Avoid_: Exam sitting, exam session

**Exam Draft**:
The one mutable authoring state of an exam before its contents and policy are
published as an exam revision.
_Avoid_: Mutable exam revision, unpublished sitting

**Exam Revision**:
An immutable published generation of an exam's authored content and delivery
configuration.
_Avoid_: Exam sitting, draft edit

**Exam Policy Set**:
The typed examination and integrity rules authored in an exam draft and frozen
in an exam revision.
_Avoid_: Deployment configuration, executable policy

**Exam Sitting**:
A scheduled delivery of one exam revision to one class.
_Avoid_: Exam version, exam session

**Exam Attempt**:
One student's private, durable body of work while participating in an exam;
its acknowledged work survives interruption and submission.
_Avoid_: Exam session, exam member

**Attempt Participation**:
One fenced, server-recognized period of continuous candidate participation in
an exam attempt.
_Avoid_: Exam attempt, connection, authentication session

**Participation Generation**:
The sequential identity of one attempt participation; once ended or expired,
it can never authorize work again.
_Avoid_: Connection number, retry count

**Participation Lease**:
The renewable server-authoritative deadline proving that one participation
generation remains current.
_Avoid_: WebSocket ping, authentication session, client timer

**Attempt Connection**:
One authenticated transport connection within an attempt participation.
_Avoid_: Exam attempt, participation

**Attempt Suspension**:
One reversible episode in which manual or policy enforcement blocks an active
exam attempt until an authorized re-allow decision.
_Avoid_: Submission, guilt finding, disconnection

**Exam Instructions**:
The authored problem statement and directions presented to students for an
exam.
_Avoid_: Subject

**Exam Resource**:
A read-only file deliberately projected inside the protected exam client to
support understanding or completion of an exam.
_Avoid_: Subject file, attachment when its exam meaning matters

**Starter Workspace**:
The optional published hierarchy of initial code and directories copied into a
new attempt workspace.
_Avoid_: Exam resource, shared workspace

**Attempt Workspace**:
The isolated, remotely authoritative hierarchy of mutable working files
belonging to one exam attempt.
_Avoid_: Shared workspace, local folder

**Execution Environment**:
The isolated, non-authoritative projection of one attempt workspace in which
a candidate may use an attempt terminal.
_Avoid_: Code runner, coderunner, microVM, sandbox, local folder

**Attempt Terminal**:
One interactive PTY attached to an execution environment for one exam
attempt.
_Avoid_: SSH session, local terminal, code runner

**Execution Profile**:
The authored, revision-frozen choice of whether an exam offers an attempt
terminal, which catalog image it uses, and which network mode applies.
_Avoid_: Devcontainer, Dockerfile, deployment configuration, executable policy

**Execution Image**:
A named, installation-provided guest runtime a creator may select in an
execution profile.
_Avoid_: Dockerfile, devcontainer, rootfs

**Workspace Entry**:
A stable logical file or directory in one attempt workspace whose identity
survives path changes.
_Avoid_: File entry, storage object, path

**Workspace Content Version**:
An opaque comparison token for one acknowledged current content state of a
workspace file; it is not a retained file revision.
_Avoid_: File revision, timestamp

**Integrity Evidence**:
Bounded, purpose-specific material retained to support an integrity flag while
preserving its provenance, uncertainty, and known gaps.
_Avoid_: Audit log, cheating proof

**Integrity Flag**:
A recorded indication of suspected examination-rule violation; it is evidence
for review, not a finding of guilt.
_Avoid_: Cheating verdict, automatic violation

**Connection Loss**:
The server-confirmed expiry of the current participation lease for an active
exam attempt.
_Avoid_: One failed request, WebSocket ping failure, guilt finding

**Focus Loss**:
A bounded client observation that the protected exam experience lacked focus
for a qualifying duration.
_Avoid_: Cheating verdict, connection loss

**Submission**:
A single immutable manifest sealing the exact acknowledged workspace state at
the end of one exam attempt.
_Avoid_: Attempt, copied workspace, grade

**Submission Review**:
The integrity decisions and manager remarks associated with one submission;
it contains no structured academic grade or outcome.
_Avoid_: Grade, rubric, submission

**Exam Manager**:
The exam creator or a teacher explicitly granted equal authority to manage one
exam; system-administrator override is not membership in this set.
_Avoid_: Proctor, grader

## File content

**File Content**:
The durable bytes and immutable representations belonging to file revisions,
including any bounded normalized or extracted forms derived from those bytes.
Whether content may be stored, searched, retained, or exposed remains meaning
owned by the file entry's application purpose.
_Avoid_: Live workspace state, authorization policy, search permission

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
The mutable, normalized, case-sensitive POSIX-relative location of a workspace
entry within one attempt workspace.
_Avoid_: Workspace identity, VFS path, object key

**Default Profile Picture**:
The system-generated, permanently retained fallback image belonging to one
user when no custom profile picture is active.
_Avoid_: Placeholder URL, shared avatar

## Durable work

**Job**:
A finite body of background work whose progress and outcome are retained and
which may continue through interruption or retry.
_Avoid_: Runtime loop, goroutine, recurring service

**Job Attempt**:
One recorded try to complete a job; several job attempts may belong to the
same job. It is distinct from an exam attempt.
_Avoid_: Exam Attempt, Job

## Identity and access

**User**:
A login-capable Proctor account containing profile and account state, but not
credentials, affiliations, roles, or permissions.
_Avoid_: Person, account when referring specifically to the Proctor identity

**User Settings Document**:
One portable, user-owned source document containing client presentation
preferences; it grants no capability and belongs to neither deployment
configuration nor an exam workspace.
_Avoid_: IDE preferences file, workspace settings, deployment configuration

**Affiliation**:
A time-bounded, non-exclusive relationship between a user and the institution,
such as student, teacher, staff member, or external collaborator.
_Avoid_: User type, role

**External Identity**:
A link between a user and an opaque subject asserted by a configured external
identity provider.
_Avoid_: SSO user, provider account

**Access Policy**:
The revisioned institution application policy selecting which configured
authentication and account-admission capabilities are currently available.
_Avoid_: Authentication mode, deployment configuration, client settings

**Invitation**:
A durable pre-user authorization for one person to claim an account and one
explicit institutional relationship or scoped role package.
_Avoid_: User token, pending user, email message

**Desktop Authorization**:
One short-lived browser-to-desktop handoff that may create a Proctor desktop
session after exact callback, state, and proof verification.
_Avoid_: Device token, external-provider token, desktop login page

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
