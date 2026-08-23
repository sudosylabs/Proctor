# HTTP contract conventions

[`../openapi.json`](../openapi.json) is the reviewed public HTTP contract.
Its coverage began with the migrated Academic Unit reference slice and now
includes Institution, Programme, Programme Level, Academic Period, Class,
Affiliation, Academic Unit Member, Class Member enrollment, Exam authoring,
User profiles, account enablement, administrative Session operations, Role
administration, Role Binding administration, Audit listing, and installation
bootstrap without weakening existing contracts.

Use the Academic Unit slice as the conceptual pattern for later capabilities:

- define request and response DTOs in the owning transport file;
- map DTOs explicitly to application commands and results;
- register every route with one authentication requirement and repeat that
  value in OpenAPI's `x-proctor-auth` extension;
- use closed request and response schemas unless a named extension object is
  intentionally open;
- document stable application error codes in `x-proctor-error-codes`, map each
  code to a declared HTTP response, and return RFC 9457 Problem Details;
- add an agreement test that compares registered route/auth metadata, DTO JSON
  fields, success schemas, and public errors with OpenAPI;
- preserve characterized v1 behavior. Contract changes are additive unless a
  new API version and migration path are introduced.

The HTTP Routing Kernel is the construction and execution boundary for HTTP
resources. Each resource declares only recognized path parameters, a narrow
application capability, typed operations, explicit authentication, and an
allowlist of public application errors. The kernel compiles the complete route
catalog before serving, validates results before applying response effects,
owns ordinary response and Problem Details writing, and fails closed when an
operation returns an undeclared error. Redirects, bounded uploads, and binary
downloads use named protocol-specific results recorded in the manifest rather
than an unrestricted response-writer escape hatch. The WebSocket handshake is
the only raw response exception: it is a named, session-authenticated upgrade
operation whose parameters, request metadata, and pre-upgrade failures remain
kernel-owned. After a successful upgrade, the sibling transport owns the
connection lifecycle. The catalog exposes no mutable router or late-registration
seam.

Catalog completion exposed two existing pre-upgrade outcomes that the prior
OpenAPI entry omitted: invalid origin (403) and unavailable WebSocket service
(503). Their declaration is an additive documentation correction for existing
runtime behavior, not a new failure mode.

## Access Policy and public discovery

`GET /api/v1/discovery` is the versioned, unauthenticated, same-origin server
discovery document. It returns only the canonical origin, installation and
Institution presentation, current policy revision, enabled public capability
flags, safe provider descriptors selected by both live deployment configuration
and Access Policy, and the supported desktop-authorization protocol range. It
never returns provider admission rules, local-invitation credential policy,
mail capability, secrets, redirect URIs, claim rules, or recipient data. The
response is `no-store`.

`GET /api/v1/auth/providers` applies the same current-policy selection to the
live configured provider catalog. Configured but policy-disabled providers are
omitted; a policy read failure fails closed with `authentication.internal`
rather than returning the deployment catalog.

The provider callback resolves accounts only by the exact configured provider
ID and opaque subject. Unlinked `linked_only` and ordinary unclaimed
`invitation_required` callbacks return the same bounded account-not-linked
outcome. Auto-provision email collisions return the bounded account-conflict
outcome without identifying the existing User. Neither response exposes
provider claims, subjects, account identifiers, admission rules, or eligibility
details.

`POST /api/v1/auth/providers/{provider_id}/login` is the invitation-bound
start. Its strict JSON body requires `invitation_claim`; the raw bearer claim is
never accepted in query parameters, copied into provider `state`, logged,
audited, or returned. `GET` on the same path remains the ordinary claim-free
start. A valid claimed flow terminally accepts the exact Invitation package and
links the proved immutable provider subject in one Store transaction. It does
not create an ordinary Web Session or leave a relationship-free User behind.

## Desktop browser authorization

`POST /api/v1/auth/desktop/authorizations`,
`POST /api/v1/auth/desktop/authorizations/cancel`, and
`POST /api/v1/auth/desktop/token` are public native-client protocol operations.
`POST /api/v1/auth/desktop/authorizations/approve` requires an authenticated
Web Session and the normal session-mutation CSRF proof. Every response is
`Cache-Control: no-store`.

Start accepts only an exact IP-literal loopback callback, high-entropy state,
an S256 challenge, and one configured policy-enabled authentication path. The
returned hosted authorization URL carries the transaction handle and state in
its query and a separate browser proof in its fragment. Approval returns an
exact loopback redirect whose query contains only the short-lived one-use code
and state. Exchange accepts code, state, and verifier, atomically consumes the
code, and returns the ordinary Desktop Session access/refresh response. Invalid,
expired, cancelled, mixed-up, or replayed proofs use bounded public errors and
never reveal which proof or policy check failed.

Start and exchange share the private authentication-attempt accounting but use
separate domain-qualified transaction and source counters. Accounting precedes
Start persistence and exchange audit preparation or persistence and fails
closed when its disposable backend is unavailable. Exchange rechecks that the
resolved User is active in the same PostgreSQL transaction that consumes the
code and creates the Session.

Ordinary external-provider login defaults to a Web Session and rejects an
explicit Desktop client at initiation and callback. Desktop Sessions are
issued only through this purpose-bound approval and PKCE/code exchange. The
pinned issuer is HTTPS except when composition explicitly grants a validated
localhost or literal-loopback HTTP development origin.

The runtime invokes bounded browser-authentication maintenance periodically on
every node for Desktop and hosted Invitation transactions. PostgreSQL row
locking makes concurrent invocations safe without a durable Job, Attempt,
occurrence, or permanent-deduplication ledger. Each pass terminalizes expired
pending/code-issued transactions with proof destruction and purges terminal
safe metadata after 24 hours; protocol writes do not perform opportunistic
cleanup scans.

Provider-connection redirects retain their one critical audit attempt across
the callback. Rejection, invalid assertion, and post-consumption failures
terminalize it immediately. A separate bounded, non-durable periodic task uses
PostgreSQL time and row claiming to fail abandoned expired connection attempts
and purge retained state after 24 hours, even when no authentication request is
writing. Start passes a bounded lifetime rather than a node-computed deadline;
creation, expiry, and one-use callback consumption are all evaluated against
authoritative PostgreSQL time.

The packaged runtime owns `/authorize/desktop`, but its visual flow and the
Desktop UI are deliberately absent. Their later implementation must consume
this protocol without adding provider tokens, Session credentials, or raw
proofs to URLs, logs, or audit data.

## Authentication-method lifecycle

`GET /api/v1/authentication-methods` requires an authenticated Session and
returns only whether a password exists plus safe linked-provider descriptors.
It never returns a password hash, provider subject, claims, or credentials.
`PUT` and `DELETE /api/v1/authentication-methods/password`,
`POST /api/v1/authentication-methods/providers/{provider_id}/connect`, and
`DELETE /api/v1/authentication-methods/providers/{external_identity_id}` all
require a strong, recently authenticated interactive Session. Personal Access
Tokens cannot satisfy that assurance.

Password enrollment requires current local-login policy and a verified User
mailbox. Provider connection creates a purpose-bound external-authentication
state pinned to the exact current User; only proof of the selected immutable
provider subject completes the link. Profile email or username equality never
selects a User. Removal rechecks current policy, deployment capabilities,
active User state, and another usable method in PostgreSQL, then archives the
exact method and revokes only Sessions authenticated through it. Responses,
Problem Details, logs, and audit values expose neither provider subjects nor
credential material.

`GET /api/v1/access-policy` requires `access_policy.view` and returns the full
policy, at most the newest 100 applied transition facts, and safe live provider
and durable-mail capability metadata. Personal Access Tokens are forbidden by
the action definition. `POST /api/v1/access-policy/preflight` and
`PUT /api/v1/access-policy` require an interactive strong, recently
authenticated Session and `access_policy.manage`; both accept the same complete
closed settings object whose booleans and `provider_admissions` are required
and non-null, exact positive `expected_revision`, and required
one-shot `revoke_existing_sessions` choice. The replacement also requires
`Idempotency-Key`; exact lost-response replay returns the retained response
before current-revision checks, while reuse with different settings or a
different revocation choice is an idempotency conflict.

Preflight reports a non-null blocker list without mutation. Replacement repeats
the blocker and revision checks in the authoritative PostgreSQL transaction,
commits the durable audit and bounded transition history with the singleton
policy, and only then publishes a best-effort realtime event containing the new
revision. Stable blocker codes cover unavailable providers, unsupported
auto-provisioning, disabled or unhealthy durable invitation delivery, and loss of the
last usable System Administrator login path. Provider and mail deployment
configuration remain process-owned and secrets never enter these DTOs.

## Scoped User and audit visibility

`GET /api/v1/users` and `GET /api/v1/users/{user_id}` derive directory
visibility from current Academic Unit membership, Class membership, or Role
Binding within the caller's authorized subtree. Search, keyset pagination, and
exact reads apply that subtree constraint in PostgreSQL. A scoped directory
projection retains only the User identity needed for academic administration;
email and verification state, locale and timezone, login/activity state, and
disabled state remain available only to the User themself or institution-wide
`user.view`. Scoped search matches only fields present in that directory
projection, so omitted email cannot become a lookup oracle. Visibility grants
no `user.manage`, account-disable, credential, MFA, external-provider, or
settings authority. Disabled Users are absent from scoped search, exact-profile,
and profile-picture reads; `include_disabled=true` is honored only for
institution-wide visibility and therefore cannot reveal scoped disablement.

Generic User-profile PATCH cannot mutate the email address or verification
state. `PUT /api/v1/users/{user_id}/email` is the explicit strong, recent
interactive-session transition; it authorizes `user.manage`, normalizes and
uniqueness-checks the new address, marks it unverified, and durably records the
old-address warning plus new-address verification intent before returning.
`POST /api/v1/users/{user_id}/email/verify` is the distinct strong, recent
privileged override and records its own user notice. Neither endpoint accepts
an administrator identity or private reason for inclusion in mail. Both
responses are the narrow `{id, email_verified}` transition state; they never
return the target mailbox or the broader User-profile projection.

Affiliation history may be read only after the same contextual User check.
Per-User Role Binding history is filtered to authorized Academic Unit
descendants and Classes; Institution and sibling bindings are omitted.

`GET /api/v1/audits` preserves full installation history for institution-wide
`audit.view`. `academic_audit.view` instead constrains the query in PostgreSQL
to academic, Invitation, onboarding-batch, Role Binding, and User-visibility
decisions whose recorded scope resolves to an authoritative Academic Unit or
Class. An Academic Unit grant remains subtree-only. An Institution grant spans
all academic scopes but retains that scope-type fence and the closed academic
action catalog; it does not become `audit.view`. Sibling events are excluded
for subtree grants, and unrelated account, credential, MFA, provider, mail,
and security events are always excluded. The response also omits Session,
request, node, authentication, IP-address, User-Agent, and private audit-value
metadata.

## Public local registration

`POST /api/v1/auth/register` is a public strict-JSON API for the local
self-registration capability advertised by discovery. It accepts only
`username`, `email`, and `password`; the successful response is an empty
no-store `202`, including a syntactically valid duplicate request. The route
returns the bounded `authentication.registration.*` and shared authentication
rate-limit vocabulary and never projects a User, mailbox, password, raw
verification credential, or internal uniqueness outcome.

The application accounts attempts under the canonical normalized mailbox and
private source dimensions before preparing the account. The named PostgreSQL
transition rechecks both current public-registration and local-enrollment
policy and atomically creates only the unverified local User, password,
settings, default-picture Job, safe audit, target-bound verification token,
frozen encrypted credential delivery, and delivery Job. It creates no
Affiliation, membership, or Role Binding. The packaged runtime owns `/register`,
but this API does not implement or claim its deferred visual flow.

## Student Class Invitations

`POST /api/v1/classes/{class_id}/invitations/student` requires an authenticated
principal and independently authorizes both `invitation.create` and
`class.members.manage` against the exact Class. Its closed request carries the
target mailbox, optional effective bounds, and bounded profile suggestions.
The `201` response is a safe package projection: it omits the mailbox, claim
digest, raw claim, and action URL. The raw 256-bit claim exists only while the
application renders and seals the transactional message whose action is
`/join#token=...`.

`POST /api/v1/invitations/student-class/accept` is public because possession of
that claim proves access to the invited mailbox. The raw claim and password are
request-only credentials and never appear in responses, Problem Details,
ordinary logs, audit values, or reports. The response identifies the resolved
User and committed Invitation/relationship records by ID only, plus whether
the result was an exact replay. It contains no mailbox, verification,
login/activity, disablement, or other account-profile metadata and issues no
Session.
Invalid, expired, conflicting, disabled-policy, and lost-authority outcomes use
the bounded `invitation.*` vocabulary and do not disclose which internal check
failed. The packaged runtime owns `/join` and its nonvisual fragment bootstrap;
the visual acceptance flow remains part of the server-hosted design-system
phase.

## Teacher Academic Unit Invitations

`POST /api/v1/academic-units/{academic_unit_id}/invitations/teacher` requires
an authenticated principal and authorizes `invitation.create`,
`academic_unit.members.manage`, and delegation of every action in the selected
custom Role at the exact Academic Unit. Its closed request freezes the
recipient, Role, canonical action snapshot, effective bounds, and bounded
profile suggestions. The safe `201` projection contains the Academic Unit,
Role, and actions but never the mailbox, claim digest, raw claim, or action URL.

`POST /api/v1/invitations/teacher-academic-unit/accept` is public and applies
the same claim/password secrecy and bounded-error rules as student acceptance.
Its purpose-specific result identifies the User, Affiliation, Academic Unit
membership, package-origin Role Binding, and Invitation by ID only, plus exact
replay status. It issues no Session. The visual `/join` flow remains deferred.

## Scoped Role Invitations

`POST /api/v1/academic-units/{academic_unit_id}/invitations/role` requires an
authenticated principal and authorizes `invitation.create`,
`role_binding.manage`, and delegation of every action in the selected Role at
the exact Academic Unit. `POST
/api/v1/institutions/{institution_id}/invitations/role` applies the same exact
delegation checks and additionally requires a strong, recent interactive
Session; a Personal Access Token cannot issue an Institution Role Invitation.
Both safe issue responses omit the recipient mailbox and every form of the raw
or hashed claim.

`POST /api/v1/invitations/academic-unit-role/accept` and `POST
/api/v1/invitations/institution-role/accept` require an authenticated Session.
The Invitation claim proves control of the invited mailbox and binds the
acceptance to that Session's exact canonical User; matching an account email is
never used to select or modify a User. Acceptance creates only a missing
compatible Role Binding, or reuses an already-satisfied one, then consumes the
Invitation atomically. It does not change the User profile or canonical email,
create an Affiliation or Academic Unit membership, attach a credential, issue
a Session, or prepare welcome or acceptance mail. The response contains only
the User, Invitation, Role Binding, and replay identifiers. A replay by that
same User returns the exact result; a different User or incompatible package
receives a bounded `invitation.*` outcome without consuming the Invitation.
The visual `/join` flow remains deferred.

## Hosted browser Invitation handoff

`POST /api/v1/auth/browser/invitations` is the public strict-JSON exchange that
supports the future visual `/join` flow. Its request contains only `claim`.
The raw claim is request-only credential material: it is rate-accounted, never
returned, logged, audited, or placed in a query string, and the endpoint
responds with `Cache-Control: no-store`. The application accepts only a pending,
unexpired Invitation with an unexpired intended relationship and maps its
closed purpose to either the `account` or `session` acceptance requirement.

The `201` response contains a random public `handle`, the Invitation `purpose`,
the closed `requirement`, and `expires_at`. A distinct random browser proof is
set as a host-only, HttpOnly, SameSite=Lax cookie scoped to
`/api/v1/auth/browser/invitations`; it is Secure outside the explicit loopback
HTTP development mode. The named creation aggregate locks and rechecks the
Invitation and computes one authoritative PostgreSQL time. The transaction
deadline is the earliest of five minutes from that time, Invitation expiry,
and intended relationship end. PostgreSQL retains only hashes of the handle,
proof, and Invitation claim together with the exact Invitation and installation
origin.

`POST /api/v1/auth/browser/invitations/accept` is public and accepts the public
handle plus the same closed local-account fields as the purpose-specific
student and teacher acceptance operations. It also requires the browser-proof
cookie and is valid only for the `account` purposes. `POST
/api/v1/auth/browser/invitations/accept-session` accepts only the handle,
requires an authenticated Web Session under the ordinary browser-cookie/CSRF
or bearer rules, requires the same proof cookie, and is valid only for the
Academic Unit and Institution Role `session` purposes. A purpose mismatch,
missing or duplicate cookie, invalid proof pair, expiry, or reuse receives the
bounded `invitation.invalid` outcome without disclosing the failed check.

Acceptance repeats the same policy, authority, target, account, and package
checks as the purpose-specific operation. Invitation consumption, relationship
or Role Binding creation, audit, and browser-transaction completion commit in
one named PostgreSQL aggregate. Concurrent exact acceptance can return the
ordinary acceptance replay projection, but no transaction proof is reusable
after completion: terminalization clears every stored proof hash. A successful
response clears the browser-proof cookie, uses `Cache-Control: no-store`, and
returns only the existing purpose-specific safe acceptance projection.

## Invitation administration

`GET /api/v1/invitations` and `GET
/api/v1/invitations/{invitation_id}` require an authenticated principal and
authorize `invitation.view` before persistence applies the resulting
Institution, Academic Unit subtree, and exact Class visibility constraint.
The list is keyset-paginated with a default limit of 50 and maximum of 200;
purpose, lifecycle state, normalized recipient email, target ID, and creation
time bounds are optional server-side filters. The opaque cursor binds the
exclusive `(created_at, id)` boundary. Out-of-scope detail reads are
indistinguishable from missing Invitations.

The administration projection exposes the approved recipient email, immutable
package, inviter and accepted User IDs, revision, timestamps, and newest safe
delivery summary. It never exposes a raw or hashed claim, action URL, rendered
mail or encrypted payload, provider identity, Message-ID, Job identity,
transport response, or internal failure detail.

`POST /api/v1/invitations/{invitation_id}/resend`, `/revoke`, and
`/replacement` require `invitation.manage` visibility and an
`expected_revision`. Resend preserves the immutable package and absolute
Invitation expiry while rotating the one-use claim and atomically replacing
unsent credential mail. Revocation immediately terminalizes the Invitation,
suppresses unsent credential mail, and queues a semantic revocation notice only
when an Invitation credential delivery was SMTP Accepted. Replacement creates
a new typed Invitation and supersedes the old one atomically; it repeats the
same target, delegation, assurance, and package validation required by direct
issue. Institution Role replacement therefore requires a strong, recent
interactive Session.

## JSON Invitation batches

`POST /api/v1/invitation-batches` requires an authenticated principal, a
required `Idempotency-Key`, and `onboarding_batch.manage` at the declared exact
scope before any row is inspected. The strict body declares one operation from
`student_class.create`, `teacher_academic_unit.create`,
`academic_unit_role.create`, `institution_role.create`, `resend`, or `revoke`;
one matching Institution, Academic Unit, or Class scope; and one to 200 items.
Each item carries a required stable `key` that is unique within the request;
combining it with the batch header keeps reconnect recovery stable even when
the client reorders rows. Repeated item keys make every affected row invalid,
so no ambiguous row identity executes. It is not
a generic command envelope, and fields that do not apply to the declared
operation fail only that row.

Rows execute in order and independently through their corresponding single
Invitation use case. Each row repeats current authorization, target and
delegation checks, credential assurance, audit, mail atomicity, and PostgreSQL
authority. One failure does not roll back prior rows. Repeated email/purpose/
target create rows or repeated lifecycle targets select the smallest stable
item key as the canonical row; every other row durably returns
`onboarding_batch.duplicate` without executing twice. Role-package work
requires a strong, recent interactive Session; PATs remain limited to ordinary
student/teacher onboarding authorized by both their ceiling and current Role
Binding.

The no-store `200` response preserves input order and contains only the item
index, `succeeded`, `no_op`, or `failed` status, an Invitation ID for success or
no-op, and one closed public error code for failure, plus bounded aggregate
counts. It contains no recipient, claim, rendered mail, provider identity,
private error, authorization detail, or delivery internals. Exact retries of a
stable item key return committed rows as `no_op`; reusing that key for changed
input is a per-row `idempotency.conflict`.
Retained outcomes contain only their disposition, Invitation identity, and the
already-approved bounded delivery summary. They contain no recipient package,
raw or hashed claim, rendered mail, or transport secret, and replay resolves
before a new claim or mail candidate is prepared.

## JSON existing-User academic administration batches

`POST /api/v1/academic-administration-batches` requires an authenticated
principal, a required `Idempotency-Key`, and `onboarding_batch.manage` at one
exact Institution, Academic Unit, or Class scope. Its closed operation union is
Affiliation add/end, Academic Unit membership add/end, Class enroll/end/
transfer, Role Binding create/end, User enable/disable, and selected-User
Session revocation. One to 200 request-unique item keys identify independent
rows; repeated keys invalidate every affected row.

Every row invokes the corresponding ordinary single-item use case and named
aggregate transaction. Current action/scope authorization, target visibility,
Role delegation, assurance, audit, mail, PostgreSQL authority, idempotency, and
conflict behavior therefore remain authoritative per row. Role Binding,
account-state, and Session operations require a strong, recent interactive
Session. Unit-scoped work cannot operate outside the caller's visible subtree;
global account intervention, credential attachment or removal, identity
linking, MFA removal, and deletion are not batch operations.

The no-store `200` projection preserves input order and exposes only index,
`succeeded`, `no_op`, or `failed`, the created or affected resource ID, one
closed public code, and bounded counts. Exact retries and already-satisfied
effects are explicit `no_op`; changed reuse conflicts at that item. Each
successful row retains only its minimal resource identity and disposition in
the same transaction as the ordinary mutation and audit. A later row failure
never rolls back completed rows; compensation is an explicit inverse ordinary
operation (for example membership end or User enable), never an implicit Job
rollback.

## CSV onboarding imports

`POST /api/v1/onboarding-imports` is a bounded streaming `text/csv` upload for
an authenticated principal with `onboarding_batch.manage` at the declared
exact scope. Query parameters select the closed Invitation or existing-User
academic administration import mode and external Class, Academic Unit,
Institution, and optional teacher Role target. The route
accepts at most 10 MiB; parsing and full row validation run asynchronously.
Original bytes are private staging material and are removed after preview
creation or cancellation.

`GET /api/v1/onboarding-imports/{id}` returns the immutable, content-digested
preview and safe row projections. `POST .../{id}/commit` requires an
`Idempotency-Key`, the exact preview digest and revision, and either
`require_all_valid` or `valid_rows_only`; it queues at most one resumable
execution Job. `POST .../{id}/cancel` cooperatively stops new rows, and
`GET .../{id}/report` downloads a `text/csv` safe-result projection. Every
operation reauthorizes; execution also revalidates authority and target
revisions per row. JSON and report responses are `no-store`, reports are
`nosniff`, and neither projection contains recipient email, CSV command fields,
raw or hashed Invitation claims, rendered mail, private errors, or User profile
fields.

## Student progression

`POST /api/v1/student-progressions` accepts exact source and destination
Academic Period and Class IDs plus one RFC 3339 effective time. It requires
`academic.progression.manage` and `class.members.manage` at both Classes,
creates no membership side effect, and queues one bounded dry-run Job. The safe
`202` projection exposes only exact target IDs, effective time, aggregate
counts, state, revision, and Job identity.

`GET /api/v1/student-progressions/{student_progression_id}` returns the
authorized immutable preview and safe per-student dispositions. `POST
.../commit` requires an `Idempotency-Key`, exact preview digest, and exact
revision and queues at most one resumable execution Job. `POST .../cancel`
prevents later row claims without reversing committed students, and `GET
.../report` returns the final formula-safe CSV result.

Every progression row reauthorizes and revalidates the frozen source and
destination Periods, Classes, source enrollment, and target User in the named
PostgreSQL aggregate transaction. Same-Period rows use the ordinary atomic
Class transfer; cross-Period rows create a new destination enrollment without
rewriting the source. Existing destination enrollment is a no-op, other
destination membership is a row conflict, and ordinary Class notification and
Sitting reconciliation semantics remain authoritative. JSON and report
responses are `no-store`, reports are `nosniff`, and generic Jobs, audits,
logs, and safe projections contain no roster, recipient, profile, or mail
payload data.

## Ownership and extension workflow

`httpapi.New` is the production construction boundary. Its broad `Options` value
exists only at composition: construction projects each application capability
through the exact narrow interface accepted by its resource constructor. A
resource may retain that focused application capability, but never `Options`,
`*API`, a router, `store.Store`, SQL, `platform.Service`, or concrete adapters.

The production route trace is intentionally linear and searchable:

~~~text
httpapi.New
    -> productionResources             (resource_catalog.go)
    -> <domain>Resource                (for example institutionResource)
    -> principalRoute/sessionRoute/... (route_definition.go)
    -> collectResources + validation   (catalog_compiler.go)
    -> application capability method   (for example GetInstitution)
~~~

This is Proctor's declarative analogue to Mattermost's root `api.go` calling
per-domain `Init*` functions. The explicit inventory retains the same useful
top-down discoverability, while resource constructors return data for a
validated, immutable catalog instead of mutating router state during setup.

To add or change a cohesive resource family:

1. define the focused application capability and transport DTO mapping beside
   the resource;
2. declare typed paths, one explicit authentication requirement per operation,
   typed ordinary or reviewed protocol results, and the complete public-error
   allowlist;
3. add the resource constructor once to `productionResources` in
   `resource_catalog.go`;
4. update the checked-in OpenAPI operation and declare independently reviewed
   operation, authentication, error, and ordinary DTO/schema expectations
   through the shared agreement-test module; and
5. keep exceptional protocol and compatibility assertions—including headers,
   binary responses, query parameters, forbidden fields, and legacy response
   shapes—explicit beside the owning resource suite.

The agreement-test module owns portable document loading, runtime-path
normalization, deterministic operation comparison, security, request and
success references, public-error parity, Problem Details, and ordinary
DTO/schema agreement. Runtime routes and OpenAPI never generate the expected
contracts they are checked against.

Package initialization, mutable router access, late registration, arbitrary
path regular expressions, and direct persistence or platform access are not
extension mechanisms. `API.Routes` is a defensive manifest projection for
agreement and diagnostics; callers cannot mutate dispatch through it.

These reviewed v1 shapes are frozen compatibility exceptions, not target
patterns:

- its v1 PATCH DTO uses pointers, so omitted and explicit `null` currently have
  the same meaning. Later slices must use the architecture's `Optional[T]`
  representation when those states differ; do not copy the pointer shape;
- its v1 collection response is a bare JSON array. New collection contracts
  use an object with non-null `items` and, where applicable, `next_cursor`;
- Role PATCH uses pointers, so omitted and explicit `null` leave each mutable
  field unchanged while empty strings or arrays are present and validated.
  Later slices use `Optional[T]` when omission and null have different meaning.

The agreement test records these exceptions so migration cannot silently
change existing clients. It does not make them conventions for new endpoints.

The Exam catalog's optional `q` parameter is a literal case-insensitive
substring match against the current Draft title. SQL wildcard characters have
no special meaning, and the current authorization constraint is applied in
PostgreSQL before results are returned.

The Exam catalog's `next_cursor` is an opaque URL-safe token whose private
payload includes a cursor version, exact update time, and Exam identity.
Clients must return it unchanged. Malformed cursors, trailing payload, and
unsupported versions are invalid requests; versionless tokens emitted before
the cursor version was added remain accepted during v1. Clients never
construct or inspect the payload.

The Exam Manager catalog follows the same opaque-cursor rule with its own
versioned grant-time and User-identity payload. It is ordered by grant time and
User identity, returns relationship provenance and creator/owner indicators,
and never expands User profiles.


The Exam Revision catalog is ordered by immutable Revision number and identity,
both descending. Its `next_cursor` is a versioned opaque URL-safe token carrying
that tuple; clients return it unchanged, and malformed, trailing, versionless,
or unsupported payloads are invalid requests. Publication uses
`POST /api/v1/exams/{exam_id}/revisions`, requires `Idempotency-Key`, and accepts
only the positive `expected_draft_revision` fence. Collection and exact reads
return bounded publication metadata: identity, number, source Draft revision,
title, policy and content digests, the frozen Exam Capacity Policy, aggregate
resource/Starter Workspace counts, publisher, time, base Revision, and
publication kind. They never return
instructions, canonical policy bytes, resource metadata or content identities,
Starter Workspace paths, object identities, or source bytes.
Publication returns `exam.revision.capacity_exceeded` as a conflict when the
current Institution policy was lowered below retained Draft content; managers
must remove or repair that content before retrying.

`PUT /api/v1/exams/{exam_id}/draft/execution-profile` replaces the complete
Draft execution choice under the normal manager authorization, Draft revision
fence, audit, and required idempotency contract. Its strict JSON body contains
`expected_draft_revision`, `enabled`, `image`, and `network`. Disabled profiles
carry an empty image and `none`; enabled profiles require a bounded catalog
image identifier and either `none` or `allowlist`. Publication freezes the
exact profile and digest into the immutable Exam Revision. Live correction
cannot change it.

`GET /api/v1/exams/{exam_id}/draft/execution-images` applies the same Exam
authoring authorization and returns only sorted, deduplicated image ids and
their supported `none`/`allowlist` modes. Host ids, addresses, credentials,
release versions, and capacity are not public API fields.

## Exam Sitting schedule

An Exam Sitting delivers one immutable Exam Revision to one exact Class over a
half-open scheduled interval. The manager surface covers pre-open scheduling
and the explicit live lifecycle transitions:

| Method and path | Request | Success |
| --- | --- | --- |
| `POST /api/v1/exams/{exam_id}/sittings` | exact Revision, Class, start, and end | `201` Sitting |
| `GET /api/v1/exams/{exam_id}/sittings` | optional bounded filters and cursor | bounded Sitting page |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}` | none | exact Sitting |
| `PATCH /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}` | expected Sitting revision and at least one non-null schedule field | updated Sitting |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/cancel` | expected Sitting revision and private reason | canceled Sitting |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/pause` | expected Sitting revision and private reason | paused Sitting |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/resume` | expected Sitting revision and private reason | resumed Sitting |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/extend` | expected Sitting revision, later RFC 3339 end, and private reason | extended Sitting |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/close` | expected Sitting revision and private reason | Closing Sitting |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/no-shows` | optional bounded limit and opaque cursor | candidate identities with no Attempt |

Every route requires an authenticated principal. All seven mutations require
`Idempotency-Key`; their bodies are closed, duplicate-free JSON objects.
Schedule instants are RFC 3339 values and start must precede end. PATCH
distinguishes omission from presence and rejects explicit `null` for Revision,
Class, start, and end.
Each private manager reason must be valid UTF-8, already trimmed, between
1 and 1,000 Unicode scalar values, and at most 4,000 encoded bytes.
Pause, resume, extension, and early close lose to the PostgreSQL deadline fence
at or after the scheduled end. Extension must move the end later and remain
inside the current Class Academic Period. Archived Exams still permit pause
and early close to reduce capability, but reject resume and extension.

The no-show view is available only while the Sitting is Closing or Closed and
uses the same current manager/override authorization as the Sitting read. It is
derived from Class membership active at the Sitting's authoritative
`opened_at`, excludes every candidate who created an Attempt, and never creates
an Attempt or Submission. Results contain only `candidate_user_id`, default to
50, accept at most 200, are ordered by that stable identity, and use an opaque
versioned cursor. The response is `no-store`.

The list defaults to 50 items and accepts at most 200. It can filter by one
`class_id`, repeated deduplicated `state` values (at most the six defined
states), and a paired `ends_after`/`starts_before` overlap interval. Results are
ordered by scheduled start then Sitting identity, both descending. Its opaque
Raw URL-safe cursor is versioned and carries that exact tuple; clients return
it unchanged.

Responses expose only the Sitting identity, Exam/Revision/Class identities,
schedule, state, lifecycle times, candidate-safe reason code, and optimistic
revision. Private manager reasons, authorization decisions,
audit provenance, and authored Exam content are never returned. All JSON
responses are `no-store`; there is no delete operation.

## Exam resource and Starter Workspace content

Exam Resource and Starter Workspace operations are purpose-specific authoring
surfaces. Every route requires an authenticated principal and applies current
Exam management authorization. Every mutation requires `Idempotency-Key` and
the current `expected_draft_revision`; JSON bodies are closed objects and
reject unknown fields.

| Method and path | Request | Success |
| --- | --- | --- |
| `GET /api/v1/exams/{exam_id}/draft/resources` | none | complete ordered resource catalog |
| `POST /api/v1/exams/{exam_id}/draft/resources` | metadata-first multipart upload | `201` resource |
| `PATCH /api/v1/exams/{exam_id}/draft/resources/{exam_resource_id}` | strict metadata JSON | resource |
| `PUT /api/v1/exams/{exam_id}/draft/resources/order` | strict complete-order JSON | ordered catalog |
| `PUT /api/v1/exams/{exam_id}/draft/resources/{exam_resource_id}/content` | metadata-first multipart replacement | resource |
| `DELETE /api/v1/exams/{exam_id}/draft/resources/{exam_resource_id}` | strict revision-fence JSON | `204` |
| `GET /api/v1/exams/{exam_id}/draft/resources/{exam_resource_id}/content` | optional `If-None-Match` | protected inline bytes or `304` |
| `GET /api/v1/exams/{exam_id}/draft/starter-workspace` | none | complete manifest |
| `POST /api/v1/exams/{exam_id}/draft/starter-workspace/directories` | strict path JSON | `201` directory |
| `POST /api/v1/exams/{exam_id}/draft/starter-workspace/files` | metadata-first multipart upload | `201` file |
| `PATCH /api/v1/exams/{exam_id}/draft/starter-workspace/entries/{starter_workspace_entry_id}` | strict destination-path JSON | moved entry |
| `PUT /api/v1/exams/{exam_id}/draft/starter-workspace/files/{starter_workspace_entry_id}/content` | metadata-first multipart replacement | file |
| `DELETE /api/v1/exams/{exam_id}/draft/starter-workspace/entries/{starter_workspace_entry_id}` | strict revision-fence JSON | `204` |
| `GET /api/v1/exams/{exam_id}/draft/starter-workspace/files/{starter_workspace_entry_id}/content` | optional `If-None-Match` | protected inline bytes or `304` |

`PATCH /api/v1/institution` manages `exam_capacity` as one complete five-field
policy covering resource count/bytes and Workspace entry/file/total bytes.
Omission or explicit `null` leaves the policy unchanged; a present object
replaces every field and is checked against fixed server safety ceilings.
The authorized Exam Draft response also exposes the current policy for
authoring guidance; each mutation still rechecks PostgreSQL and does not trust
the earlier projection.

Each multipart body contains exactly two parts in order: a non-file `metadata`
part containing one strict JSON object of at most 32 KiB, followed by a
`content` part. Duplicate metadata fields, trailing JSON, missing or reordered
parts, and additional parts are invalid. `size` and lowercase hexadecimal
`sha256` are required metadata. Starter Workspace replacements additionally
require the exact current `expected_content_version`; a stale version is a
conflict. A Workspace Content Version is an opaque 26-character URL-safe
comparison token matching `[A-Za-z0-9_-]{26}`. It is not an entity ID and
clients return it unchanged. The hard route body limit is 100 MiB plus 64 KiB
of multipart overhead. PostgreSQL authoritatively applies the current
Institution policy to each Exam Resource or Starter Workspace finalization;
the default per-file limit remains 10 MiB.

Protected content responses set a strong checksum ETag,
`X-Content-Type-Options: nosniff`, and no `Content-Disposition` header. Exam
Resources use `Cache-Control: private, max-age=300`; mutable Starter Workspace
files use `Cache-Control: private, no-store`. These operations provide only an
authorized in-application content stream. Metadata never exposes VFS paths,
object keys, or public URLs, and the API defines no download/export operation.

## Live Sitting correction

An Exam Manager corrects one Open or Paused Sitting through a two-step,
purpose-bound surface. Both operations require `Idempotency-Key` and current
Exam/Sitting management authorization.

| Method and path | Request | Success |
| --- | --- | --- |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/correction-resource-stages` | metadata-first multipart upload | `201` ready stage metadata |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/corrections` | strict correction JSON | `201` immutable Revision and retarget result |

Stage metadata names the exact `base_revision_id`, target kind (`addition` or
`replacement`), optional replacement resource identity, media type, explicit
size including zero, and lowercase SHA-256 digest. The multipart shape and hard
100 MiB plus 64 KiB body limit are identical to Exam Resource authoring. The
base Revision's frozen resource-byte limit is authoritative when applying the
correction. A
successful response contains only the purpose-bound stage and resource
identities, authoritative ready rendition metadata, and expiry. File Entry,
File Revision, rendition, upload-lease, VFS key, path, and URL identities never
cross the transport boundary.

The apply body carries the expected Sitting revision, expected current Exam
Revision, required private manager reason, optional `instructions_markdown`,
and a required complete resource manifest bounded by the base Revision's frozen
resource-count limit, within the server ceiling of 100 items. Omitting
`instructions_markdown` preserves it; a present empty string clears it and
explicit `null` is invalid. Resource omission means removal, array order
becomes position, an item without `stage_id` retains the exact base content,
and an item with `stage_id` selects that ready purpose-bound stage. Resource
and non-empty stage identities are unique. Unknown or duplicate JSON members,
including policy, Starter Workspace, future-default, and schedule fields, are
invalid. The response excludes the private reason, authored content, stages,
and storage identities. This surface adds no content download route; current
authoritative presentation remains a later protected delivery seam.

## Idempotent commands

Routes declare `none`, `optional`, or `required` idempotency in the immutable
catalog and repeat non-`none` policy in OpenAPI. Existing v1 operations may add
optional support; making the header required needs a new compatible contract.
The initial optional operations are `POST /api/v1/academic-periods` and
`POST /api/v1/academic-units`. New Exam creation, archive, Draft text editing,
and Draft Focus Loss policy replacement require the header because their
contracts are idempotent from introduction. Adding or removing an Exam Manager
and transferring Exam ownership also require it; every request carries the
expected Exam revision in its strict JSON body, including DELETE.

`Idempotency-Key` is one case-sensitive opaque value of 1–128 characters from
letters, digits, `-`, `.`, `_`, and `~`. Transport rejects malformed values;
the application fingerprints a versioned canonical command, and the named
Store mutation atomically commits the successful application outcome. A
matching replay repeats authentication, authorization, and audit but not the
mutation or post-commit effects. Raw keys, fingerprints, commands, stored
outcomes, and replay state never enter public fields or ordinary telemetry.

Correct transport ownership:

```go
type createAcademicUnitRequest struct {
    Name        string `json:"name"`
    DisplayName string `json:"display_name"`
}

unit, err := academicUnits.CreateAcademicUnit(ctx, invocation, command)
writeJSON(writer, http.StatusCreated, academicUnitResponseFromModel(unit))
```

Incorrect domain serialization and transport policy:

```go
var unit model.AcademicUnit
decodeJSON(writer, request, &unit, "update")
store.AcademicUnit().Update(request.Context(), &unit)
```

## Exam Attempt protected access and management

Exam Managers use the Exam/Sitting-scoped Attempt catalog and exact read;
candidates use Attempt-scoped protected delivery routes:

| Method and path | Authentication | Success |
| --- | --- | --- |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts` | principal plus current management authorization | bounded manager-safe Attempt page |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts/{exam_attempt_id}` | principal plus current management authorization | exact manager-safe Attempt |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts/{exam_attempt_id}/reallow` | principal plus current management authorization and required idempotency key | exact suspension re-allowed |
| `GET /api/v1/exam-attempts/{exam_attempt_id}/presentation` | Session plus Attempt credential and Connection | current instructions/resource metadata |
| `GET /api/v1/exam-attempts/{exam_attempt_id}/workspace` | Session plus Attempt credential and Connection | bounded logical Workspace page |
| `GET /api/v1/exam-attempts/{exam_attempt_id}/workspace/changes` | Session plus Attempt credential and Connection | bounded journal page or explicit full-refresh signal |
| `POST /api/v1/exam-attempts/{exam_attempt_id}/workspace/directories` | Session plus Attempt credential and Connection; required idempotency | acknowledged directory and Cursor |
| `POST /api/v1/exam-attempts/{exam_attempt_id}/workspace/files` | Session plus Attempt credential and Connection; required idempotency | staged and acknowledged file and Cursor |
| `PATCH /api/v1/exam-attempts/{exam_attempt_id}/workspace/entries/{attempt_workspace_entry_id}` | Session plus Attempt credential and Connection; required idempotency | acknowledged rename/move and Cursor |
| `PUT /api/v1/exam-attempts/{exam_attempt_id}/workspace/files/{attempt_workspace_entry_id}/content` | Session plus Attempt credential and Connection; required idempotency | acknowledged replacement and Cursor |
| `DELETE /api/v1/exam-attempts/{exam_attempt_id}/workspace/entries/{attempt_workspace_entry_id}` | Session plus Attempt credential and Connection; required idempotency | acknowledged deletion and Cursor |
| `GET /api/v1/exam-attempts/{exam_attempt_id}/resources/{exam_resource_id}/content` | Session plus Attempt credential and Connection | protected inline bytes or `304` |
| `GET /api/v1/exam-attempts/{exam_attempt_id}/workspace/files/{attempt_workspace_entry_id}/content` | Session plus Attempt credential and Connection | protected inline bytes or `304` |
| `POST /api/v1/exam-attempts/{exam_attempt_id}/submissions` | Session plus Attempt credential and Connection; required idempotency | `201` candidate-safe retained receipt |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts/{exam_attempt_id}/submissions/{submission_id}` | principal plus current Submission-view authorization | protected immutable Submission header |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts/{exam_attempt_id}/submissions/{submission_id}/manifest` | principal plus current Submission-view authorization | bounded immutable manifest page |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts/{exam_attempt_id}/submissions/{submission_id}/files/{attempt_workspace_entry_id}/content` | principal plus current Submission-view authorization | protected inline sealed bytes or `304` |
| `GET /api/v1/submissions/{submission_id}/integrity-flags` | principal plus current Submission-view authorization | bounded safe Flag summaries |
| `GET /api/v1/submissions/{submission_id}/integrity-flags/{integrity_flag_id}/evidence` | principal plus current Submission-view authorization | bounded purpose-specific evidence |
| `GET /api/v1/submissions/{submission_id}/integrity-discrepancies` | principal plus current Submission-view authorization | bounded post-collection discrepancies |
| `GET /api/v1/submissions/{submission_id}/review` | principal plus current Submission-view authorization | manager Review snapshot including authorized private fields |
| `PUT /api/v1/submissions/{submission_id}/review` | principal plus current Submission-review authorization and required idempotency | created or revised draft Review |
| `PUT /api/v1/submissions/{submission_id}/review/decisions/{integrity_flag_id}` | principal plus current Submission-review authorization and required idempotency | one created or revision-fenced Flag decision |
| `POST /api/v1/submissions/{submission_id}/review/finalize` | principal plus current Submission-review authorization and required idempotency | immutable finalized Review inventory |
| `POST /api/v1/submissions/{submission_id}/review/release` | principal plus current Submission-release authorization and required idempotency | explicitly released Review state |
| `GET /api/v1/exam-attempts/{exam_attempt_id}/result` | authenticated candidate Session relationship | narrow released result or concealed not found |

Every candidate route requires exactly one
`X-Proctor-Attempt-Credential` header containing the canonical 32-byte Raw
URL-safe base64 continuity credential and one
`X-Proctor-Attempt-Connection-ID` header. The application hashes the credential
immediately; the Store also binds candidate reads to the authenticated Session
ID and durable open Connection. Neither header is accepted from a URL, echoed,
logged, included in Problem Details, or persisted in raw form. Missing,
duplicate, whitespace-altered, or malformed values are invalid requests.
Every Workspace mutation also carries the non-secret `participation_id` and
`generation` returned by the latest successful connect response. File writes
use metadata-first, exactly-two-part multipart bodies bounded to a hard 100 MiB
plus 64 KiB; the admission Revision's frozen Workspace file limit is
authoritative and may be lower. Other mutations use duplicate-free strict JSON.
The access selectors
are reauthorized on every write but are excluded from the Attempt-scoped
idempotency fingerprint so an exact command can recover across reconnect.
Malformed paths, cursors, limits, states, or protection headers return
`request.invalid` (`400`). Missing or mismatched Attempt, candidate,
Participation, credential, Connection, or manager-visible target is concealed
as `resource.not_found` (`404`). A presently unreadable Sitting or blocked
Attempt returns its stable safe `exam.attempt.*` conflict (`409`); dependency
failure is `exam.attempt.unavailable` (`500`). No error distinguishes which
sensitive selector failed.

Submission repeats `participation_id` and `generation`, and requires the
expected acknowledged Workspace Cursor plus the client's final Focus Loss
sequence; zero is a valid sequence. It rechecks current Class membership and
all active continuity selectors inside the named atomic Store operation. Both
an initial commit and its exact replay return `201`, but replay suppresses
post-commit realtime and unbind effects. The candidate receipt contains only
Submission and Attempt identities, `submitted` state, Workspace Cursor,
manifest digest, and server submission time. Candidates have no Submission
browse or content route.

Manager reads authorize the canonical Submission identity before testing the
nested Exam, Sitting, and Attempt ownership path, so a mismatch is concealed
as not found. Authorization rechecks the current Exam Manager relationship and
Submission-view scope or override. Manifest pagination is ordered by stable
Entry identity; its opaque cursor contains that identity only, never a path.
Manifest responses expose immutable logical paths and bounded content metadata
but no starter/Attempt object identity or VFS selector. Sealed content is
streamed from its retained storage origin through the protected application
content capability; it is never represented by a signed or public URL.

Manager results are ordered by Attempt creation time and identity descending.
Candidate Workspace results are ordered by canonical path and entry identity
ascending. Both catalogs default to 50 and accept at most 200 items, using
distinct opaque versioned keyset cursors. JSON is `no-store`. Candidate binary
content is inline, `private, no-store`, `nosniff`, and conditionally readable
with a strong ETag; it has no `Content-Disposition`, public URL, object key, or
download/export contract.

Manager Submission JSON is `no-store`. Submission file content has the same
`private, no-store`, `nosniff`, strong-ETag, no-`Content-Disposition` contract.

Integrity Review JSON is `no-store`. Manager list cursors are opaque,
versioned, identity-only tokens and each page is limited to 200 items. Evidence
and discrepancy records expose only their purpose-specific bounded fields;
they do not expose a credential hash, Session identity, Connection identity,
Workspace selector, or arbitrary client payload. Decision private rationale
and Review manager notes are returned only on manager-authorized Review
responses and are excluded from ordinary audit values and realtime events.
Mutation JSON is strict, duplicate-free, closed, and requires
`Idempotency-Key`.

The existing authenticated Attempt WebSocket carries the terminal; there is no
candidate-to-host endpoint. After `exam_attempt.connect`, the client may send
`exam_attempt.terminal.open` with the current generation, continuity
credential, and non-zero window, followed by bounded base64
`exam_attempt.terminal.input`, `exam_attempt.terminal.resize`, and
`exam_attempt.terminal.close` actions. The server emits bounded base64
`exam_attempt.terminal.output` and a terminal `exam_attempt.terminal.closed`
event. PTY events are deliberately excluded from replay history and terminal
bytes, credentials, host identities, and paths are never logged or audited.

The candidate result route is concealed until explicit release. Its response
contains only Review, Submission, and Attempt identities, sanitized approved
student-facing Markdown, and release time. It never contains private manager
notes or rationale, flags, evidence, discrepancies, sealed manifest/content,
grade, score, rubric, pass/fail, or another candidate identity.

Manager projections omit credential hashes, Session identities, and private
reasons. A suspended Attempt exposes its private-free active Suspension
identity so an authoritative refetch can drive exact re-allow after missed
realtime delivery. Candidate
presentation exposes the admission Revision only as provenance while title,
instructions, resources, and resource content resolve from the Sitting's
current Revision. Its single Focus Loss field is the required
`focus_loss_collection_enabled` boolean telling the trusted client whether to
collect and transmit observations; minimum duration, incident count, window,
outcome, and raw policy never enter the candidate projection. Workspace pages
expose logical entries and content versions, never starter/Attempt object
identities or VFS keys.

Re-allow requires the exact active Suspension identity, expected Attempt
revision, and a trimmed private manager reason. The reason is retained only in
the durable suspension provenance: it is absent from the JSON response,
ordinary audit values, logs, realtime events, and idempotent command outcome.
Re-allow creates neither a continuity credential nor a Participation; the
candidate must establish a fresh authenticated connection generation.

The OpenAPI document describes the wire contract only. It does not generate or
dictate domain models, application commands, persistence rows, or handlers.

## Controlled transactional-mail tracer

`GET /api/v1/mail/keys` requires `mail.rekey` and a strong, recently
authenticated interactive Session. It returns only the configured primary key
identity, the durable required-primary fence when one exists, and a bounded
list of active key identities with aggregate reference counts. PATs cannot
satisfy the route. Ciphertext, plaintext, key material, envelope metadata,
recipient data, and payload identities are never projected.

`POST /api/v1/mail/rekey` requires the same `mail.rekey` authorization and
strong-recent Session assurance, accepts exactly one `retiring_key_id`, and
returns `202` with the safe durable Job identity, primary and retiring key
identities, and creation time. The command, audit event, required-primary
write fence, and Job commit atomically before workers are woken. The critical
audit attempt is persisted before the Store mutation; successful terminal
completion is part of that same Job-and-fence transaction, while conflicts or
persistence failures complete the attempt separately and fail closed. A
concurrent rotation returns `mail.rekey.conflict`; unknown, malformed, or
current-primary retiring identities return `mail.rekey.invalid`. The request
and response never carry encryption key material.

`GET /api/v1/mail/rekey/{job_id}` requires the same `mail.rekey` authorization
and strong-recent Session assurance. It returns the closed Job state and safe
timestamps, attempt policy, primary and retiring key identities, processed and
re-encrypted aggregate counts, optional typed `reencrypting` progress, and a
typed zero-reference retirement proof only after success. It never returns raw
Job command, checkpoint, or result documents, key material, ciphertext,
payload identities, recipients, or rendered message content. An incompatible
old-primary worker Attempt remains available through the ordinary Job Attempt
history as a safe capability diagnostic.

`POST /api/v1/mail/test` accepts no request body, recipient, or message copy.
It requires a recent interactive Session and `mail.manage`, and returns `202`
with the safe durable Delivery projection after the occurrence, encrypted
frozen payload, audit event, and Job commit atomically. A PAT cannot satisfy
the route assurance. The recipient is always the principal's own verified
address.

`GET /api/v1/mail/deliveries/{mail_delivery_id}` requires `mail.view` and
returns the same `no-store` projection. It contains safe identities, template
key and digest, masked recipient, state, timestamps, stable Message-ID, attempt
count, and closed public failure code. It never contains a full address,
subject, rendered alternatives, template data, ciphertext, credentials, SMTP
configuration, or provider response. `accepted` means SMTP accepted DATA, not
that the message reached an inbox.

`GET /api/v1/mail/deliveries` requires `mail.view` and returns the same safe
projection through a bounded, opaque-cursor collection. Repeated `state` and
`template_key` filters and optional millisecond `created_after` and
`created_before` bounds are applied in persistence; `limit` defaults to 50 and
is bounded at 200. Authorization completes before any delivery is inspected.

`GET /api/v1/mail/metrics` requires `mail.view` and returns only bounded
template/state/public-outcome aggregates, attempts, latency, queue count and
age, truncation, and the closed mail-health code. It never exposes recipients,
message content, payloads, provider responses, or delivery identifiers.

`POST /api/v1/mail/deliveries/{mail_delivery_id}/cancel` and `/retry` accept no
body and require `mail.manage` plus a recent interactive Session; PATs cannot
satisfy either route. Cancellation is limited to queued or retry-waiting work.
Retry is limited to failed, unexpired, still-relevant work. Both operations
revision-fence and mutate the existing Delivery and its Job atomically, retain
the same recipient, occurrence, and Message-ID, and complete a payload-free
audit event in the same transaction. Sending or terminal races return
`mail.conflict` and no endpoint creates arbitrary mail.
