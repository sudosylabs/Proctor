# HTTP contract conventions

The human-authored [`../openapi/`](../openapi/) modules and generated,
reviewed [`../openapi.json`](../openapi.json) artifact together form the public
HTTP contract.
Its coverage began with the migrated Academic Unit reference slice and now
includes Institution, Programme, Programme Level, Academic Period, Class,
Affiliation, Academic Unit Member, Class Member enrollment, Exam authoring,
User profiles, account enablement, administrative Session operations, Role
administration, Role Binding administration, Audit listing, and installation
bootstrap without weakening existing contracts.

Routes that perform password hashing or verification, Resource content
validation, or profile-picture processing declare `service.busy`. A full
node-local work pool rejects expensive work immediately with status 503 and
`Retry-After: 1`, without issuing the requested credential or publishing the
requested content. Clients preserve any required idempotency key when retrying;
an Upload Lease or correction stage reserved before content admission remains
subject to its existing retry and cleanup rules. Refusals use bounded metrics
rather than one error log per request and do not change readiness.

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
- preserve characterized behavior deliberately. Before the first supported
  release, the current `/api/v1` contract may change in place when the server,
  generated contract, and compatible clients are activated together; no
  parallel version is created solely for pre-release compatibility. After the
  first supported release, established meanings evolve additively unless an
  explicit compatibility decision introduces another version and migration.

## API reference data

Exam agreement document codecs use the
[`canonicaljson` byte contract](../internal/canonicaljson/README.md) before
canonical storage and hashing. Their raw JSON validation rejects duplicate
members, malformed Unicode, and non-integral or unsafe number spellings before
typed decoding. Packaged Desktop registry selectors use `fnv1a64:` plus sixteen
lowercase hex digits; configuration manifests and cryptographic content
fingerprints retain SHA-256. A registry selector is not authenticity evidence.
Individual operation schemas still own field sets, presence, bounds and the
exact semantic fields selected for each digest.

Attempt Configuration admission uses one `configuration_manifest_fingerprint`,
not a supported-manifest list. Its closed proposed object contains exact
presentation, approved command/keybinding IDs and original build/target,
registry and User Settings provenance. The server freezes it once with an opaque
revision; its SHA-256 covers every candidate field. Pixel line height is bounded
independently of font size. Boolean fields must be present and non-null. Frozen
storage and privileged recovery preserve provenance, while candidate runtime
responses expose only presentation, approved IDs, configuration revision and
digest. The reduced response deliberately lacks the provenance needed to
recompute that full digest. Newer compatible builds retain the original object.

`server/openapi/` is the human authoring interface for wire shapes and API
reference data. Its small resource-oriented YAML modules co-locate paths with
definitions that have one owner; area and root shared modules contain only
definitions with real shared consumers. `server/openapi.json` is the
deterministic reviewed artifact for runtime agreement, API consumers, and
documentation generation. It is never hand-edited. The compiler discovers and
collision-safely merges normal partial OpenAPI documents, resolves references,
enforces universal operation metadata, validates OpenAPI 3.1, and fails when
the artifact is stale. See the
[`OpenAPI authoring guide`](../openapi/README.md).

Every operation has exactly one declared product-area tag, a non-empty summary,
explicit `security`, and explicit `x-proctor-auth`,
`x-proctor-error-codes`, and `x-proctor-idempotency` metadata. Idempotency is
always one of `none`, `optional`, or `required`; absence does not mean `none`.
Tags follow durable user and domain capabilities rather than Go packages,
transport resources, or source-file layout. Top-level OpenAPI tag declarations
own the taxonomy names and their descriptions.

Public behavioral prose belongs in OpenAPI `description` fields. Checked shell
examples use `x-codeSamples`; request examples belong on their OpenAPI media
types. Examples use synthetic Institutions, Users, Exams, and identifiers, and
replace every credential or secret with an explicit placeholder. General Go
comments, test fixtures, and generated renderer output do not silently become
public API guarantees.

The documentation package's `npm run audit:openapi` command emits the current
coverage and taxonomy as JSON. Both that audit and the server schema validation
fail on duplicate operation IDs, missing summaries, undeclared or multiple
tags, or missing Proctor contract extensions. The audit additionally enforces
the rich-description, parameter, request-body, and redacted-example standard on
the representative documentation pilot before that standard is expanded
across the whole contract.

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

`httpapi.New` requires the complete resource capability graph and an explicit
WebSocket transport. It resolves and validates those dependencies once before
catalog compilation; production has no no-op transport or application-capability
fallback. `resource_catalog.go` is a pure ordered resource inventory and must
contain neither dependency selection nor runtime type assertions.

Catalog completion exposed two existing pre-upgrade outcomes that the prior
OpenAPI entry omitted: invalid origin (403) and unavailable WebSocket service
(503). Their declaration is an additive documentation correction for existing
runtime behavior, not a new failure mode.

## Institution Retention Policy

`GET /api/v1/retention-policy` reads the Institution's authoritative revisioned
configuration under `retention_policy.view`; Personal Access Tokens are
forbidden. `PUT` requires a strong recent system-administrator Session,
`retention_policy.manage`, `Idempotency-Key`, and the editor's
`expected_revision`. All seven day-count settings are required non-null
integers. Export retention accepts zero when unconfigured or 1–7 days; the other
settings accept zero through 36,500. Zero means no configured expiry or grace
period, never immediate deletion; initial settings are all zero.

The policy, successful critical audit, and idempotent result commit together.
A stale revision conflicts; every write, including exact replay and no-op,
rechecks the current access credential, protected administrator binding and
strong recent Session assurance at the post-lock database time. Exact replay
is freshly audited; a no-op preserves revision, timestamps, and approval. `candidate_notices` is an
optional non-null boolean defaulting to false. Responses are private/no-store;
`automatic_deletion_enabled` is derived from the separate approved control for
that policy revision. It is never a writable policy field. Actual settings
changes pause cleanup and cancel outstanding grace.

Retention preview/control and record APIs accept interactive Sessions. Only
strong recent protected administrator approval with a fresh one-hour preview,
matching policy and control revisions, positive grace, and an idempotency key
can enable cleanup. Record pages count Submissions and project work, integrity,
Browser Activity and security operational categories without answer content. Audit and receipt previews expose
only counts and dependency blockers. Durable own-recipient notices use a
separate opaque retirement cursor; they grant no underlying examination access
and have no read-acknowledgement effect. Every projection is private/no-store.

Category retirement and current-key byte absence are distinct states. Work
reads after integrity-only retirement explicitly report retired integrity and
omit its private detail. Expiring receipts never remove permanent retirement
fences. Hold release and records completion remain separate authorized domain
operations; shared published material remains protected. The bounded server
APIs add no broad hosted administration pages.

## Examination exports

The `exam-exports` resource owns Session-only creation, requester-only metadata,
and binary downloads for individual Submission or Sitting scope. Creation is
idempotent and returns 202; category arrays select explicit work and/or
integrity, or `browser_activity` alone. Generic work/integrity archives contain
no ordinary browsing URLs. Browser Activity archives independently require exact
current Exam Manager membership and the dedicated history action at every read
and retry. Their source protection belongs to Browser Activity retention. Ready metadata advertises the full archive SHA-256 and
length. Binary responses use a fixed opaque attachment name, private/no-store,
application/zip, checksum ETag, and nosniff; no storage URL or key is public.
Current record authority, readiness, and fixed expiry are rechecked by the use
case after storage opens and before it returns a response body. The authoritative
Store projects expiry using database time; a serving node's clock cannot extend
the lifetime. Rejection closes the pending reader, and a changed artifact cannot
reuse an earlier open. Source retirement does not invalidate the independently
verified archive,
while expiry denies new downloads even before byte cleanup. This manager
archive does not change the candidate inline-content contract or add Resource
or Starter Workspace download endpoints.

## Browser request acceptance

Before authentication or application work, unsafe requests reject a present
Origin that differs from the configured public origin and reject cross-site,
same-site, malformed, or repeated Fetch Metadata. Host and forwarded headers
cannot authorize a browser origin. Native clients may omit browser headers.
Public routes return `request.invalid`; credential-protected routes use the
existing `authentication.csrf.invalid` response. Ordinary cookie mutations
still require their signed double-submit proof.

JSON bodies require exactly one `Content-Type: application/json`, optionally
with `charset=utf-8`. Missing types, form/text media types, duplicate types,
and unsupported parameters are rejected before decoding. Multipart and binary
protocol operations retain their own media contracts. GET, HEAD, and OPTIONS
remain available for safe navigation; existing CAS and OIDC GET callbacks
retain their one-use state and browser-binding checks. A future cross-site
provider POST requires an explicit protocol design rather than a global bypass.

## Access Policy and public discovery

`GET /api/v1/discovery` is the versioned, unauthenticated, same-origin server
discovery document. It returns only the canonical origin, installation and
Institution presentation, current policy revision, enabled public capability
flags, safe provider descriptors selected by both live deployment configuration
and Access Policy, and the supported desktop-authorization protocol range. It
never returns provider admission rules, local-invitation credential policy,
mail capability, secrets, redirect URIs, claim rules, or recipient data. The
response is `no-store`.

`GET /api/v1/system/ping` separately owns product availability and
request-specific Desktop compatibility. It requires bounded Desktop release,
build ID, platform, architecture, and positive realtime-protocol selectors.
Every well-formed request returns `200` and `Cache-Control: no-store`, including
temporary unavailability, revoked or unknown builds, release/protocol mismatch,
and syntactically valid unsupported targets. Missing, duplicated, unknown, or
malformed selectors return `request.invalid`. The response carries one RFC 3339
server time, stable availability and compatibility reason codes, applicable
release/protocol bounds, and the optional bounded administrator message. It
never repeats origin, Institution, provider, Access Policy, build provenance,
or updater destinations owned elsewhere. `GET /api/v1/system/version` remains
limited to server build provenance.

Each server release owns an immutable bounded catalog of verified Desktop build
tuples: release, build ID, target, realtime protocol, Attempt Configuration
manifest fingerprint, and signed capability-matrix identity. The production
catalog remains empty until matching signed Desktop artifacts are coordinated
for activation. Compatibility is the intersection of that catalog and the
Institution's revisioned Desktop Compatibility Policy; policy may set a minimum
release, revoke at most 256 build IDs, and publish one bounded plain-text
administrator message, but it cannot authorize an unknown build. Evaluation has
no request-time dependency on central distribution and never returns an update
URL.

`GET /api/v1/desktop-compatibility-policy` requires `access_policy.view`.
Complete replacement at `PUT /api/v1/desktop-compatibility-policy` is reserved
to the protected system-administrator Role and requires an interactive strong,
recently authenticated Session, exact positive `expected_revision`, and
`Idempotency-Key`. PostgreSQL rechecks current system-administrator status in
the same transaction that commits the revision, retained idempotent outcome,
and successful audit. Audits retain only the minimum release, revoked-build
count, and whether a message is set; revoked build identities and message text
do not enter ordinary audit payloads.

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

## Bounded MFA recovery and fresh proof

The security context, setup and activation routes explicitly accept
`mfa_recovery_session_required` or `recent_mfa_recovery_session_required`.
Password and original-provider reauthentication and logout also accept the
restricted Session context. Ordinary routes fail with `authentication.invalid_token`;
clients may query the bounded MFA status to distinguish mandatory reenrollment
from a signed-out browser. Restricted Sessions retain normal cookie/CSRF rules.
MFA status projects the original password/oidc/cas method, optional provider ID,
strength, recency, service availability and recovery restriction without requiring
ordinary User-profile access. Every security response is `Cache-Control: no-store`.

`POST /api/v1/auth/reauthenticate/password` accepts only the current User's password.
The external counterpart accepts the closed task `security` or `connect-provider`,
uses the original provider and exact current identity, and sets the existing
HttpOnly browser-binding cookie. A consumed reauthentication callback returns to
that task; a failed bound proof returns a fixed `external_login=failed` fragment on
the closed reauthentication page. It carries no provider error or secret. Neither
proof endpoint automatically performs the final sensitive action.

`POST /api/v1/users/{user_id}/mfa/reset` is a separate protected administrator
operation. Its attestation reference identifies outside-Proctor verification;
passwords, identity documents, authenticator secrets and recovery codes are never
valid evidence fields. The caller cannot reset itself. Success reports only that
fresh primary proof and reenrollment are required. It exposes no reset secret or
primary-credential bypass and has no general hosted administration page.

## Desktop browser authorization

`POST /api/v1/auth/desktop/authorizations`,
`POST /api/v1/auth/desktop/authorizations/bind`, the hosted-journey context and
authentication operations, approval or cancellation, and
`POST /api/v1/auth/desktop/token` are public native-client protocol operations.
Only `POST /api/v1/auth/desktop/authorizations/authenticate/session` requires a
Web Session and normal session-mutation CSRF proof. Every response is
`Cache-Control: no-store`; the other hosted operations require the scoped
Desktop authorization browser cookie.

Start accepts only an exact IP-literal loopback callback of at most 1024 bytes,
high-entropy state, and an S256 challenge. The decimal ephemeral port may retain
leading zeroes; browser validation preserves that registered spelling. The
returned hosted authorization URL carries the
transaction handle and state in its query and a separate one-use browser proof
in its fragment. The hosted bootstrap removes the fragment and handle from
history before binding them once to a host-only, HttpOnly, SameSite=Lax cookie
scoped to the Desktop authorization route family. Subsequent context,
authentication, account reset, approval, and cancellation operations accept
only that cookie plus the exact state where terminal intent requires it.

The same-tab hosted journey may bind an existing Web Session as an identity
proof, validate a local password specifically for the transaction, or navigate
through a purpose-bound external-provider state. Local and provider paths do
not create a Web Session. Provider return resumes at
`/authorize/desktop?state=...` through the browser binding cookie. The User
must confirm the safe account/device projection and explicitly approve; “Use
another account” clears only the transaction authentication and returns to
method selection.

Approval returns an exact loopback redirect whose query contains only the
short-lived one-use code and state. Start also accepts one strict ES256 P-256
public JWK and returns a DPoP nonce. Exchange accepts code, state, verifier,
the same public JWK, exact Desktop build selectors, and a nonce-bound DPoP
proof. It repeats compatibility, atomically consumes the code, creates or
refreshes the User's Desktop Registration for that public-key thumbprint,
binds the Desktop Session to it, and returns `DPoP` access and refresh
credentials plus a Session-bound nonce. Invalid, expired, cancelled, mixed-up,
replayed, or key-mismatched proofs use bounded public errors and never reveal
which proof or policy check failed.

Every Desktop access-token request carries `Authorization: DPoP ...` and one
`DPoP` header whose proof covers the exact canonical public method and target,
access-token hash, issued time, unique proof identity, and current server
nonce. Refresh uses the same registered key and a refresh-specific proof.
Nonce challenges return only a replacement `DPoP-Nonce`; shared bounded replay
state prevents accepted proof reuse across nodes. Bearer presentation of a
Desktop credential is invalid. Private keys, proof JWTs, nonces, public-key
coordinates, and thumbprints never enter response bodies, Problem Details,
ordinary logs, or audits.

Start and exchange share the private authentication-attempt accounting but use
separate domain-qualified transaction and source counters. Accounting precedes
Start persistence and exchange audit preparation or persistence and fails
closed when its disposable backend is unavailable. Resolving an account
acquires the per-User Session lock and terminally denies the transaction when
that User has an active Attempt. Exchange acquires the same lock and repeats
that check in the transaction that would consume the code and create the
Session. Ordinary login and Attempt activation share this lock, so no
interleaving can issue a new Session after an Attempt becomes active.

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

The packaged runtime implements `/authorize/desktop`; the Desktop UI remains a
separate client concern. The hosted page consumes this protocol without adding
provider tokens, Session credentials, or raw proofs to rendered content,
storage, logs, or audit data.

`GET /api/v1/users/me/desktop-registrations` requires an interactive Session
and returns a private no-store list of only that User's bounded device/build
metadata, lifecycle state, and current-registration marker. It omits the JWK
and thumbprint. `DELETE
/api/v1/users/me/desktop-registrations/{desktop_registration_id}` requires
strong recent Session authentication and atomically revokes the Registration,
all of its live Sessions and credentials, and the audit. The irreversible
record remains visible as revoked; security effects publish only after commit.

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
`username`, `email`, required self-asserted `first_name` and `last_name`, and
`password`; `display_name` is not a public-registration field. The successful
response is an empty no-store `202`, including a syntactically valid duplicate
request. The route returns the bounded `authentication.registration.*` and
shared authentication rate-limit vocabulary and never projects a User,
mailbox, password, raw verification credential, or internal uniqueness
outcome. The personal names establish profile presentation only; they prove no
identity, affiliation, membership, or authorization.

The application accounts attempts under the canonical normalized mailbox and
private source dimensions before preparing the account. The named PostgreSQL
transition rechecks both current public-registration and local-enrollment
policy and atomically creates only the unverified local User, password,
settings, default-picture Job, safe audit, target-bound verification token,
frozen encrypted credential delivery, and delivery Job. It creates no
Affiliation, membership, or Role Binding. The packaged runtime owns `/register`,
whose hosted form consumes this API without acquiring any additional account
or authorization semantics.

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

## Academic revisions and membership pages

Academic Unit, Programme, Programme Level, Academic Period, and Class responses
include `revision`. Their PATCH commands accept an optional positive,
non-null `expected_revision`; DELETE accepts the same optional positive value
as a query parameter. Omission preserves existing clients. When supplied,
the application checks the editor's revision after authorization and the Store
fences the mutation atomically. Conflicts use each resource's existing
`*.conflict` code; a stale editor cannot update or archive newer state.

Academic Unit Member and Class Member GET collections preserve bare-array
responses. Supplying `limit` or `cursor` opts into bounded keyset paging, with
50 items by default and a maximum of 200. A continuation is returned in a
relative `Link` with `rel="next"`; no continuation means the page is final.
Ordering is the immutable `(user_id, id)` tuple. Every page freshly authorizes
the scope. Its bounded cursor fixes scope and effective time/history filters;
omitted filters inherit the cursor, while conflicting values are rejected.
Paged `history=true` cannot be combined with `active_at`. Results remain a live
view, not a retained database snapshot. Calls without paging parameters retain
their existing full-list behavior.

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

All keyset cursors use the shared bounded opaque envelope. Encoding is
deterministic canonical unpadded raw URL-safe Base64 of one strict JSON object.
Decoding rejects padded or non-canonical Base64, invalid UTF-8, non-object JSON,
duplicate, case-aliased, or unknown members, trailing values, missing required
resource/version data, overlong tokens, and versions outside the owning
resource's explicit admission set. The owning resource defines the private
keyset fields, ordering, validation, and legacy-version policy; clients return
the token unchanged. Candidate Workspace retains its established optional
`after_entry_id`: a snapshot-only cursor restarts that pinned snapshot safely,
while every server-emitted continuation includes the final entry identity.

Changes to cursor or construction behavior must keep these checks green from
`server/`:

~~~bash
go test ./httpapi
go test -race ./httpapi
go vet ./httpapi
make openapi-agreement
make architecture
~~~

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

Starter Workspace directory removal defaults to empty directories. Explicit
`recursive: true` archives the directory and every descendant atomically under
the existing `expected_draft_revision`, advances that revision once, and
preserves published and admitted content pins. The flag is invalid for files.

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
Revision, required private manager reason, explicit sorted `affected_capabilities`, optional `instructions_markdown`,
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
| `GET /api/v1/users/me/exam-activity` | interactive candidate Session | bounded self-scoped navigation page |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/candidate-statuses` | principal plus current Sitting-view authorization | bounded manager-safe candidate-status page |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts` | principal plus current management authorization | bounded manager-safe Attempt page |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts/{exam_attempt_id}` | principal plus current management authorization | exact manager-safe Attempt |
| `GET /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts/{exam_attempt_id}/browser-activity` | principal plus current exact Exam Manager membership and dedicated Browser Activity permission | bounded privacy-minimized activity page |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts/{exam_attempt_id}/end` | principal plus current management authorization and required idempotency key | candidate-safe manager-ended Submission receipt |
| `POST /api/v1/exams/{exam_id}/sittings/{exam_sitting_id}/attempts/{exam_attempt_id}/reallow` | principal plus current management authorization and required idempotency key | exact suspension re-allowed |
| `GET /api/v1/exam-attempts/{exam_attempt_id}/presentation` | Session plus Attempt credential and Connection | current instructions/resource metadata |
| `PUT /api/v1/exam-attempts/{exam_attempt_id}/corrections/{exam_revision_id}/acknowledgement` | bound registered-key Desktop Session plus Attempt credential and Connection; required idempotency | retained correction acknowledgement |
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
ID and durable open Connection. An active Attempt is bound to the exact
registered-key Desktop Session owned by its current Participation, so these
requests also require that Session's valid DPoP access credential and proof.
Neither Attempt header is accepted from a URL, echoed,
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
Directory deletion defaults to empty directories. With `recursive: true`, the
request must supply `expected_workspace_cursor` from its complete manifest
(zero is valid), omit `expected_content_version`, and target a directory. Any
intervening Workspace mutation conflicts before the subtree changes. Success
atomically removes the root and all descendants, advances the Cursor once,
and emits one journal record with `recursive: true` and the root `old_path`.
Clients remove that path and descendants separated by a slash, preserving
similar sibling prefixes. Other mutations omit this aggregate cursor fence.
The recursive flag and expected cursor are semantic idempotency inputs.
Malformed paths, cursors, limits, states, or protection headers return
`request.invalid` (`400`). Missing or mismatched Attempt, candidate,
Participation, credential, Connection, or manager-visible target is concealed
as `resource.not_found` (`404`). A presently unreadable Sitting or blocked
Attempt returns its stable safe `exam.attempt.*` conflict (`409`); dependency
failure is `exam.attempt.unavailable` (`500`). No error distinguishes which
sensitive selector failed.

Correction acknowledgement uses strict JSON containing the current
`participation_id`, `generation`, and `expected_current_revision_id`. The path
Revision must be the oldest pending required correction. Each pending required
notice blocks only its selected capabilities. Browser Policy changes require
`browser`; instructions/resources require `submission` and
`workspace`, with deliberate supersets permitted. Empty or omitted selections
are invalid, including notice-only corrections. Candidate notices carry the
immutable selection; runtime state carries `pending_correction_capabilities`.
Pending browser acknowledgement withholds usable policy content. Acknowledgement,
protected correction/resource reads and integrity delivery remain available,
including while paused. Exact replay repeats current authorization and audit
checks and returns `200` with the original acknowledgement time, fresh current
Revision and runtime capabilities. `acknowledged_at` on a notice is present
only in its acknowledged state.

Submission repeats `participation_id` and `generation`, and requires the
expected current Exam Revision, acknowledged Workspace Cursor and client's final
Focus Loss sequence; zero is valid. The server closes every live Browser source
atomically and derives Browser settlement across all Participations. Clients do
not supply a completeness claim. Pending uploads do not block sealing. The
operation checks current Class membership, the submission capability's pending
correction gates and active continuity selectors inside the named atomic Store
operation. Initial commit and exact replay return `201`; replay repeats current
authorization/audit and returns current Browser settlement while preserving the
original sealed content and suppressing duplicate mutation/unbind effects.
The candidate receipt includes Submission/Attempt identities, state, governing
Revision, Workspace Cursor, manifest digest, submission time and the five-field
`browser_activity` settlement. Its counts contain no history, URLs or evidence.
Candidates have no Submission browse or content route.

The manager `end` command requires strict JSON with the expected Attempt
revision and a private trimmed reason plus `Idempotency-Key`. Before the
Sitting deadline it may seal a Ready, Active, or Suspended Attempt and returns
the same candidate-safe receipt with `manager_ended_attempt` provenance. At or
after the deadline, scheduled Sitting closure owns `sitting_closed`
provenance. Neither response, mail, realtime event, nor ordinary audit value
contains the private reason.

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

Candidate Exam Activity and manager candidate-status collections likewise
default to 50, accept at most 200, and use opaque versioned keyset cursors.
Exam Activity is self-scoped and exposes only safe lifecycle/access state,
allowed navigation action, and candidate-safe Submission provenance. The
candidate-status board returns one common `server_time` and derives presence
from the authoritative Participation lease; it never projects a Session,
Registration, credential, Connection, evidence, private reason, or Review
decision. Manager Browser Activity uses a separate no-store keyset ordered by
receipt time, source, and sequence with the same 50/200 bounds. Each page rechecks
current exact Exam Manager membership plus the dedicated
Browser Activity view permission. No administrator or general export override
grants browser history. History-bearing export creation, replay, metadata reads
and downloads repeat this requirement. Denial remains audited and non-disclosing;
an audit failure never releases a page or archive.

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
current Revision. It also returns the immutable Attempt Configuration, a
bounded derived runtime-capability projection, the current governed Browser
Policy when enabled, and the ordered live-correction notices with their
Attempt-owned acknowledgement states. Runtime capability fields explain
whether Workspace mutation, Submission, and Browser interaction are
currently available; they do not replace authorization at each operation.
The presentation's single Focus Loss field is the required
`focus_loss_collection_enabled` boolean telling the trusted client whether to
collect and transmit observations; minimum duration, incident count, window,
outcome, and raw policy never enter the candidate projection. Workspace pages
expose logical entries and content versions, never starter/Attempt object
identities or VFS keys.

Browser Activity source creation remains the authenticated Attempt WebSocket
`exam_attempt.browser_activity.start` action. Its closed declaration binds the
Participation generation, UUIDv4 source, immutable policy Revision/digest and
initial, policy-correction or runtime-reset transition. The server derives
User, registered key and opening Session/Connection provenance. A Participation
has one initial start, at most 32 correction starts and 16 runtime-reset starts;
retries do not spend another start. A valid runtime-reset refusal still closes
its predecessor and makes browser capability temporarily unavailable.

Live events use `exam_attempt.browser_activity.append`; historical delivery uses
`POST /api/v1/exam-attempts/{exam_attempt_id}/browser-activity/sources/{source_session_id}/events`.
The HTTP operation requires an already closed source and current registered-key
ownership, rejects candidate Connection headers, and revalidates the retained
source's original policy. Both transports use one closed event codec and 1..64
sorted events bounded at 256 KiB. Source/sequence/canonical SHA-256 is the durable
per-event identity across repacking. Acknowledgements contain exactly the
submitted receipts, actual contiguous and seen sequences, settled and allocated
boundaries, terminal cutoff, earliest 32 recoverable ranges, truncation and time.
A permanent gap advances settlement without inventing a receipt.

Under that same source resource, GET returns status, GET `receipts` returns up
to 64 retained receipts with sparse sequence pagination, POST `gaps` returns a
declaration receipt and current status, POST `seal` fixes an already closed
source's final boundary, and POST `summary` returns cumulative unretained counts
and status. Every HTTP mutation requires Idempotency-Key. Status is bounded at
16 KiB; the Participation list contains at most 49 statuses in start order and
is bounded at 1 MiB. Live controls require current candidate Connection headers;
closed-source controls require the current owner key and unexpired upload
window. Neither path grants manager history access or navigation authority.

Lifecycle records omit navigation fields. Navigation records include only a
reason-minimized location and applicable rule/block reason; redirects reference
the earlier successful hop so source-rule permission is verified in sequence.
Missing provenance remains unresolved. Successful HTTPS and institution-pinned
HTTP navigation retain canonical network components. Other denied schemes keep
only their lowercase scheme with empty host/path; invalid URLs have all-empty
location components. Query, fragment, credentials, local paths, scheme payloads,
title, referrer, headers, page content, cookies, DOM and download bodies remain
outside the wire contract. Candidate browser disclosure is independently
revisioned from policy and reflects current history retention settings.

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


## Desktop security admission and recovery

A registered Desktop prepares one pending challenge per Session and Sitting at
`POST /api/v1/exam-sittings/{exam_sitting_id}/security-preflights` and submits
its minimized report to `POST /api/v1/security-preflights/{security_preflight_id}/report`.
Both require idempotency keys. Preparation supersedes the previous challenge,
has a one-second per-owner rate floor and a 120-second lifetime, and allocates
no Attempt, Workspace or Participation. Expired pending rows are reclaimed in
bounded, nonblocking pages. An exact preparation replay preserves its challenge
and policy but returns current server time. A report replay preserves its
original receipt time; it cannot extend admission freshness.

`exam_attempt.connect` requires a closed `security` union. First admission and
Ready rejoin use `kind=preflight`, `preflight_id`, and `report_digest`. The atomic
admission operation rechecks current eligibility, registered key, admitted
build/compatibility revision, current published policy and catalogs, frozen
configuration, challenge expiry, and a report receipt no older than 30 seconds.
It consumes the preflight and allocates the security stream/control owner with
the Participation. First admission rebinds policy scope to the new Attempt;
its full digest changes while its content digest remains identical. The
`security` response contains the immutable admitted binding receipt. Exact
command replay returns that receipt without consuming or reserving again.

A same-generation reconnect supplies `kind=resume`, `participation_id`,
`generation`, and `policy_digest`, alongside the existing continuity proof.
It requires the retained owning Session/key and an unexpired lease. Reusing a
consumed preflight with a different command does not substitute for resume.
`GET /api/v1/exam-attempts/{exam_attempt_id}/security-policy` recovers the exact
active receipt and original frozen configuration for the owning registered
Desktop Session, including after transport closure. It neither renews authority
nor returns an ended generation; Ready reentry uses preflight.

Before a new security owner is allocated, its closed bounded binding, latest
report, control-cache, closure, final, quota and summary slots reserve lifetime
capacity under the Attempt's 2-MiB metadata ceiling. Capacity is never refunded.
A refusal returns `exam.delivery.metadata_capacity` (HTTP 409 and the same
realtime code) with four numeric capacity fields, and leaves the preflight and
Ready state intact. It is a permanent allocation refusal, not a retryable 429.

Effective native policies have no independent expiry; the Participation lease
is the live authority. Native evidence and control processing have separate
owners and do not acquire authority merely by possessing this receipt. Release
admission remains fail closed while the production signed-artifact catalog is
empty. A signed matrix restricts claims; authenticated reports do not constitute
independent attestation of the physical OS or utility-process continuity.


The required `security_coverage` in `exam_attempt.renew` and the independent
`exam_attempt.security.update` action use one persisted processed-control boundary.
The latter carries generation, continuity_credential, and security_coverage and
never extends the lease. Both whole requests are bounded at 64 KiB. Reset faults
are processed outcomes; a stale or faulted control can accompany a valid renewal
without restoring interaction. A retained identical sequence replays its original
outcome with current gates and the latest processed sequence/digest. Changed bytes
at a retained sequence return `exam.security.control_conflict`; an unknown older
sequence returns `stale_control`. Faulted source continuity retains the last usable
source heads until a greater valid control establishes new continuity.

Active security-policy recovery includes current sources, coverage, current-head
reset receipts, and the latest processed control projection. Neither its receipt
nor a healthy historical outcome grants interaction. Workspace mutation and voluntary
Submission recheck current durable security state in addition to their existing
lifecycle and correction gates. Source reset facts have separately charged immutable
receipts; processing and current coverage occupy distinct reserved bounded slots.


Native delivery controls live under
`/api/v1/exam-attempts/{exam_attempt_id}/security-streams/{stream_id}`. Status,
exact receipt lookup, gap declarations, final declarations (`seal`), and cumulative
summaries require the source's owning User and registered key in a currently valid
Session. Live sources additionally require both existing Attempt Connection and
continuity headers. After recorded closure those headers may be omitted; omission
never selects historical authority. Status contains no unrelated append receipt.

Permanent gaps and actually received batches have distinct ledgers. The native
receive-window base is the greater of actual contiguous receipt and settled
progress, with 1,024 positions of allowance. Gap declarations are at most 8 KiB and
32 sorted disjoint nonadjacent ranges; they require Idempotency-Key and a semantic
declaration ID. Their original exact receipt survives newer progress. Final
boundaries require a closed source, at most 2 KiB, a matching declaration revision,
and Idempotency-Key. A final declaration can add at most 1,024 positions beyond
known-at-close, subject to lifetime capacity, and never extends the upload deadline.
Missing batches never acquire a fabricated content receipt.

The closure owner records the first loss of collection authority independently
of client delivery. Voluntary Submission, manager/automatic end, suspension and
lease expiry close native delivery within their lifecycle transaction. A delayed
expiry observation uses the original lease boundary. Closed status expires absent
client finalization at the server-known boundary with explicit unknown-tail state;
received content remains separately acknowledged. Summary counts and client times
are operational uncertainty. Summary writes require Idempotency-Key and atomically
complete their audit; retries repeat current ownership and upload-deadline checks.
Changed summaries have a separate five-second rate
bound, with one final-summary exception, and false completeness cannot become true.
Metadata exhaustion commits summary-only state before returning its bounded refusal;
existing closure and summary slots were reserved at admission.


Native batches are appended to
`/api/v1/exam-attempts/{exam_attempt_id}/security-batches` with Idempotency-Key.
The original closed envelope identifies the stream, Participation, generation,
security session, policy and release/matrix; retries preserve its original
prior acknowledgement. A batch contains 1..64 minimized records and is bounded
at 256 KiB before decoding. Source ranges and detector meanings are checked
against the admitted release catalog, including for historical delivery from a
new Session. No packaged detector catalog means ingestion remains unavailable.

Exact batch receipts and current progression commit with counted canonical
records, envelope, fixed metadata and pending/retained/allocated counters. Native
append uses one User/Attempt allowance across Sessions, including replay, at two
requests per second with burst eight. Pending admission preserves 320 KiB repair
headroom. Detail and position exhaustion permanently latch the applicable scope;
retries recover retained receipts without charging retained quota again. Refused
new bytes never receive a content receipt.

Occurrence interpretation waits for received or terminally omitted earlier
positions. A missing opener remains explicitly unresolved after later recovery.
Counts, condition/detector identity and source lifetimes cannot change across an
occurrence, and recurrence requires another occurrence ID. A live detailed reset
links its already accepted control edge. A new historical reset establishes only
evidence continuity, never live coverage or permission. Native operational records
remain distinct from condition projections and do not create misconduct Flags.

### Late delivery and Browser evidence review

Verified blocked redirects create Browser integrity groups only when the frozen
source rule selects `integrity_evidence` and its prior hop is verified. The group
identity is Attempt, Participation, Policy Revision and rule. The first 100
eligible detail copies are immutable, within the Attempt's 10,000-record/8-MiB
copy allowance. Further qualifying accepted events increment exact count-only
overflow. At 256 groups, one Attempt overflow counts additional verified events;
it neither creates another Flag nor guesses distinct omitted groups. Receipt
replays and unresolved redirects add no evidence. Ordinary history and copied
evidence have independent authorization and retirement purposes.

Manager Flag responses optionally include `browser` group metadata; evidence
responses optionally include the minimized `browser` copy. Review snapshots may
include `browser_evidence_overflow`. No such fields appear in candidate results.
New accepted late delivery invalidates the current Review and waiver and makes
Sitting records completion stale. The current Review advances to a withheld draft;
its previous finalization, exact identities, decision revisions and bounded
snapshot remain integrity records. An affected decision reports
`inventory_stale=true` until a manager records a new revision. Finalization cannot
accept a stale decision. The complete inventory permits 456 Flags and 30,000
copied evidence rows (the existing bounds plus the separate Browser allowance).
A finalization digest binds delivery revision and all-source Browser settlement,
including changes that add only overflow counts. Waivers expose
`inventory_invalidated`; a stale waiver cannot authorize records completion.
A later approved release has a distinct revision-derived mail occurrence; exact
retries cannot send another notice. Current candidate results are withheld while
the revised inventory awaits approval. Immutable Submission work does not change.

Owning Desktop reads `GET /exam-attempts/{exam_attempt_id}/delivery-limits` with
one owned Participation query selector. Its current-key-authorized snapshot is
bounded at 8 KiB. `POST .../delivery-limits/stop-details` requires current live
fences and Idempotency-Key and accepts only family plus
`local_loss_inventory_exhausted`. It returns `budget`, nullable `native` and a
required `browser` array for the selected current Participation, at most 49 Browser
sources, bounded at 1 MiB. This control never replenishes lifetime quotas or
changes otherwise valid participation, submission or live security authority.

Review snapshots expose `delivery_inventory_revision`, including before a Review
exists. Waiver requests compare `expected_delivery_inventory_revision` to that
current value under the Sitting and Submission fences. Omission means zero and
cannot acknowledge later delivery. Waiver responses retain the exact acknowledged
`delivery_inventory_revision` independently of their invalidation marker.

Recoverable delivery refusals can include `delivery`, bounded at 32 KiB, with
`family`, the current `budget`, and that family's `native_status` or
`browser_status`. Each projection rechecks current registered-key ownership;
a missing or retired owner, revoked Session, or invalid projection omits the
extension. Independent observations can include concurrent progress and never
acknowledge the rejected request. This extension is restricted to delivery
capacity, replay-window, detail/position, declaration, deadline and rate-limit
codes. HTTP retryable capacity/rate refusals return `Retry-After: 1`; equivalent
live Browser WebSocket refusals return `retry_after_seconds: 1`. Native status,
receipt and target reads do not persist lifecycle or interpretation changes.

Live controls accept at most 50 delivery watermarks: the admitted native stream
and owned Browser sources in the current Participation. A foreign selector or
fabricated contiguous acknowledgement rejects the complete control. Owned closed
Browser sources and position exhaustion return per-source rejections without
rejecting otherwise valid coverage or lease renewal. Allocations charge the
existing lifetime counters and never fabricate receipts; replays resolve compact
immutable source slots without allocating again or moving a closure boundary.

### Native condition review

`GET /submissions/{submission_id}/native-conditions` uses the existing detailed
Submission integrity-view authorization, a separate cursor kind, and pages of at
most 100 immutable occurrence transitions. Each record carries the admitted
stream, Participation, policy/release provenance, original receipt time and
ordered interpretation time. Unresolved openers remain explicit. Conditions
require no Flag, decision row, or automatic consequence; operational health,
permission, reset and gap records never enter this evidence endpoint.

The manager Review projection includes `native_conditions` (retained transition
count and inventory digest). Finalization binds that exact native inventory
alongside the existing review inventory. New interpreted native condition
material invalidates a finalized Review, release and waiver; exact receipt
replays do not. Historical finalization records retain the prior digest and
count without copying an unbounded list into each snapshot. Integrity exports
include these purpose-specific records. Integrity retirement deletes native
condition evidence and its delivery copies and prevents fresh condition intake;
security operational retirement cannot delete independent integrity evidence.

The Sitting candidate-status board may include `native_security`: a retained
condition-record count, whether live coverage is available, and a bounded list
of source health/permission/completeness. It excludes occurrence, detector,
condition and source-instance identities and all detailed native records.

Detail intake has a bounded per-node database admission budget shared by native
and Browser delivery and by live and historical transports. It uses at most half
the configured SQL connections (and at most eight concurrent append operations),
leaving pool capacity for delivery status, declarations and live controls. A full
admission budget refuses intake with `exam.delivery.append_rate_limited` and
one-second retry advice before reserving a database connection; the durable
User/Attempt token bucket still applies to admitted requests. Per-Attempt database
serialization remains authoritative and uses bounded query deadlines.

On WebSocket, Browser append has one running worker and one waiting request per
connection. A full append queue returns the same bounded retry refusal. The reader
continues handling renewal and security updates independently. Disconnect cancels
and joins the append worker before Attempt finalization; queued bytes grant no
receipt or renewed authority. Existing Hub connection limits and outbound
backpressure bound the aggregate transport queues.

Candidate control abuse protection is per node and per authenticated User across
Sessions. Live renewal/security updates and delivery recovery/status/declarations
(including preflight prepare/report) each have their own 32 concurrent workers and 20 requests/second allowance with a
burst of 40. Each lane retains at most 4,096 User allowances, reclaiming idle slots
after a minute. Saturation returns `exam.delivery.control_rate_limited`, HTTP 429
or a WebSocket error with one-second retry advice, before domain work or recovery
reads. The limits are load protection, not a lease or authorization decision;
ordinary current authority checks still run for admitted work. Detail quotas,
append rate buckets and historical work cannot spend the live-control allowance.
