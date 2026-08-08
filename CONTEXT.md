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
