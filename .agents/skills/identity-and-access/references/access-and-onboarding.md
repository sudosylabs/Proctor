# Access and onboarding reference

This reference owns the accepted installation-access, hosted-authentication,
account-admission, invitation, and administrative-onboarding design. Code and
tests remain authoritative for discoverable implementation state.

## Boundary and model

One installation represents one Institution. One person has one Proctor
`User`; authentication methods attach credentials or external identities to
that User rather than creating method-specific accounts.

- `AccessPolicy` is the revisioned singleton application policy governing
  local login, public local registration, local credential enrollment,
  invitation admission, configured provider admission, and desktop
  authorization.
- `PasswordCredential` is optional. Disabling local login makes existing
  credentials unusable without deleting them.
- `ExternalIdentity` is keyed by immutable `(provider ID, opaque subject)`.
  Email and username are profile or discovery hints, never external identity
  keys.
- `Invitation` is a durable pre-User aggregate. Purpose-specific account
  tokens remain separate and are not reused for invitations.
- `BrowserAuthenticationTransaction` binds one browser authentication to one
  closed purpose. A successful provider callback does not decide its terminal
  effect.
- `DesktopAuthorization` is a short-lived public-client handoff that exchanges
  a one-use code and S256 PKCE verifier for one ordinary Desktop Session.
- `OnboardingBatch` and its retained row outcomes describe finite
  administrative work; generic Jobs execute it but do not replace its domain
  state.

Credentials, affiliation, Academic Unit membership, Class membership, and
Role Binding remain separate concepts. Membership never grants authority by
itself. Provider claims never directly create affiliations, memberships, or
Role Bindings.

## Bootstrap and initial policy

Bootstrap remains an explicit one-time named aggregate rather than a first-user
side effect. A high-entropy deployment-owned bootstrap secret protects the
public setup operation. Production or any network-accessible listener requires
an explicitly configured secret. An explicit development mode bound only to
loopback may generate one temporary value and display it once to the
controlling terminal, outside structured logs. A known development value is
invalid when a non-loopback listener is present.

The serialized bootstrap transaction consumes the secret and creates all of
the following atomically:

- Institution profile;
- first local administrator and encoded password;
- protected `system_admin` role and Institution binding;
- Access Policy revision 1;
- installation marker and successful audit.

Losing or failed attempts leave no partial state. Exact idempotent replay
returns the retained result, conflicting reuse fails, and bootstrap never
mints a special Session. Before initialization an operator may replace the
configured secret; after successful initialization bootstrap configuration
cannot reopen setup.

The initial Access Policy enables local login and desktop authorization,
allows local credential creation only through bootstrap or an applicable
invitation, makes later local account creation invitation-only, disables
anonymous registration, and leaves configured external providers unavailable
until an administrator explicitly enables their policy. The bootstrap email
is unverified because deployment authority does not prove mailbox control.

The first administrator follows ordinary local login, verifies their email,
tests mail, enables and tests an external provider, deliberately links their
identity, optionally appoints another administrator, and only then may disable
local login. Server invariants, not setup-page advice, prevent removal of the
last usable authentication path for active system administrators.

Host-level recovery is an offline operator operation, never a network route.
It may re-enable local login or rotate one designated system administrator's
password after explicit installation confirmation. It mints no Session,
silently removes no MFA requirement, keeps secrets outside ordinary output,
and leaves a pending durable security record before normal service resumes.
Every application node must be stopped while the command runs. Every backend,
including the single-node local backend, commits and renews a short-lived
PostgreSQL-clocked serving lease before workers or listeners start. Recovery
shares the lease mutation fence and refuses to run while any lease remains
unexpired; after an ungraceful node loss the operator must wait for that lease
to expire. A renewal failure makes the node unready and force-stops serving
before its last lease can expire; that failed lease is not withdrawn early and
expires naturally as an additional fail-safe. Constructing the inert recovery
graph creates no lease.
Password input comes from the command's private input channel rather than an
argument; a rotation is an availability repair, so it preserves existing
Sessions and the operator may use ordinary Session administration after
regaining access if compromise is suspected.

Local-login recovery advances the Access Policy revision under the same
system-administrator authentication-path fence as ordinary policy and
credential mutations. It is deliberately not attributed to the target User in
the actor-bearing ordinary replacement history. Instead, the dedicated
pending record retains the exact from/to revisions and changed field, and the
next startup converts that fact into an actor-free ordinary audit event before
jobs or network transports start. Consequently the bounded replacement
history may contain a revision gap only for this documented host operation.

## Access Policy and deployment configuration

Access Policy is durable application data separate from both deployment
configuration and the Institution profile. It has an opaque ID, monotonic
revision, lifecycle timestamps, optimistic replacement, and append-only
bounded transition history. History records actor, revisions, safe changed
field names, time, and outcome without provider secrets or recipient data.

Deployment configuration owns provider protocol material and process
capability: issuer and service URLs, client credentials, certificates, claim
mappings, SMTP, listener/public origin, and resource limits. Institution
administrators may select policy only among configured, validated providers;
ordinary administration never reads or replaces deployment secrets.

Policy is composable rather than a `local`, `external`, or `hybrid` enum:

- local password login is enabled or disabled;
- public local registration is enabled or disabled;
- invitation acceptance may offer a local credential, an external provider, or
  only the methods permitted by current policy;
- each configured provider is independently disabled, `linked_only`,
  `invitation_required`, or `auto_provision`;
- provider admission may apply configured eligibility predicates, but those
  predicates grant no academic relationship or permission.

Ordinary provider login resolves only the immutable `(provider ID, opaque
subject)` link. `linked_only` therefore admits an existing link and otherwise
returns a bounded unlinked-account outcome. `invitation_required` also admits
an existing link, but an ordinary unlinked login has no Invitation claim and
fails the same way. An invitation-bound start instead accepts the raw claim in
a strict request body, resolves its digest to one current pending Invitation,
and stores only that Invitation ID in a short-lived, one-use state bound to the
exact provider and browser proof; the raw claim never enters provider state,
logs, audits, or output. The callback rechecks the current PostgreSQL policy,
deployment capability, Invitation state and bounds, verified normalized
provider mailbox, email uniqueness, and immutable-subject uniqueness. It may
resolve the canonical active User by the Invitation mailbox or create that User
when the purpose permits it. One named PostgreSQL aggregate then links the
immutable provider subject, applies the exact frozen relationship/Role package,
accepts the Invitation, suppresses its obsolete credential delivery, records
safe audit and semantic mail, and commits all of those effects together. A
different verified provider mailbox is acceptable only when it is not owned by
another User; it never selects or merges an account. This closed purpose does
not create an ordinary Web Session or leave a relationship-free User behind.
`auto_provision` may create a User only when
the current validated provider capability permits it and the provider adapter
has accepted its configured eligibility predicate. That User starts with no
Affiliation, Academic Unit or Class membership, Role Binding, or trusted-device
state. Profile email and username are candidate fields, never lookup or merge
keys. Later claim changes resolve the established subject without overwriting
the User profile.

The terminal identity-resolution aggregate intersects the exact current
PostgreSQL admission mode with an immutable deployment-capability snapshot.
A node whose current configuration omits the provider fails closed before
resolving or provisioning, while the durable policy entry and identity link
remain unchanged for safe restoration of the same provider ID.

User interfaces may derive labels such as local-only or hybrid, but those
labels never drive application policy. Removing a provider from deployment
configuration preserves its policy entry and identity links while making it
effectively unavailable. Restoring the same immutable provider ID restores
eligibility after validation.

Every terminal login, recovery, invitation, identity-link, and desktop-code
exchange rechecks current policy. Updates use expected revision and a preflight
that describes safe blockers, then recheck invariants atomically at commit.
Access Policy replacement, User disablement, and ending a `system_admin`
binding share one PostgreSQL transaction fence. While holding it, each
path-removing mutation re-reads current policy and counts only active
administrators with an unarchived password permitted by local-login policy or
an unarchived External Identity for an exact policy-enabled provider present
in the immutable current validated deployment-capability snapshot supplied by
the application. A node that omits a provider fails closed for that external
path; durable policy and identity entries remain intact so restoring the same
provider ID restores eligibility. Concurrent mutations therefore cannot each
validate against a path the other removes.
Each authorized replacement records a durable attempt before mutation;
revision, blocker, persistence, and idempotency failures complete that attempt
as failed, while fresh success and exact replay complete their respective
attempts atomically with the retained outcome. Replay audit identifies the
original attempt without re-emitting Session-revocation effects.
Disabling an authentication method prevents new use and accepts an explicit
choice whether to revoke existing Sessions authenticated by that method. It
does not silently couple planned maintenance to Session revocation. The
preflight and replacement request field is `revoke_existing_sessions`; it is a
one-shot transition choice included in idempotency, audit, and history rather
than a persisted policy setting. When selected, the policy replacement and
revocation of active Sessions authenticated by methods newly disabled by that exact
transition commit atomically; Sessions using retained methods are untouched.
External Sessions therefore retain their method or protocol, immutable
configured provider ID, and exact `ExternalIdentityID`. Unlinking one identity
revokes only Sessions carrying that exact identity, including Desktop Sessions
approved through such a Web Session; other identities for the same provider
remain usable. Only local Sessions keep both external fields empty. Proctor has
no released schema requiring an upgrade from provider-only external Sessions:
the pre-release baseline is resettable, and development databases using an
earlier shape must be recreated. Runtime policy and revocation never guess a
provider or identity from `authentication_method`.

Policy changes commit before cache invalidation and a content-free realtime
event carrying only the new revision. PostgreSQL remains authoritative on every
node. Mail outage after activation degrades the mail subsystem; enabling
invitation-required admission is rejected unless a durable
invitation-delivery path is configured and healthy.

## Public discovery and server origin

The signed-out desktop accepts a user-entered server URL but treats it as
untrusted. Production uses HTTPS; explicit loopback development may use HTTP.
A versioned same-origin public discovery document returns only:

- canonical server origin and installation identity;
- initialized state;
- safe Institution name and branding;
- Access Policy revision and available authentication capabilities;
- enabled providers with ID, display name, and type;
- supported desktop-authorization protocol and compatibility bounds.

The v1 public document is `GET /api/v1/discovery`. Administrative policy read,
preflight, and revision-fenced replacement are respectively
`GET /api/v1/access-policy`, `POST /api/v1/access-policy/preflight`, and
`PUT /api/v1/access-policy`; the replacement requires `Idempotency-Key`.

The client pins the canonical origin for the complete transaction and refuses
mix-up with another installation or issuer. Discovery never returns provider
secrets, claim policy, recipient data, private readiness detail, or arbitrary
navigation destinations.

## Hosted pages and browser authentication

Proctor Desktop contains no login, registration, password, invitation, or
provider-authentication form. Proctor Server eventually hosts:

- `/setup`;
- `/login`;
- `/register`;
- `/authorize/desktop`;
- `/join`;
- `/account/forgot-password`;
- `/account/reset-password`;
- `/account/verify-email`;
- `/account/connect-provider`;
- `/authorization/complete`.

When public local registration is enabled, `/register` creates a User with a
local credential and begins ordinary mailbox verification but grants no
Affiliation, membership, or Role Binding. When it is disabled, the same route
explains that an Invitation is required without accepting an email or revealing
Invitation state. `/join` remains the purpose-bearing account and relationship
claim surface.

Hybrid login presents local and enabled provider choices. Several external
providers produce a chooser. One external-only provider still requires an
explicit Continue action before redirect, avoiding silent loops. Password
recovery is hidden when local login is unavailable while its API retains a
generic response.

Visual page and component implementation remains deliberately deferred. The
server-hosted [webapp design system](../../../../webapp/DESIGN_SYSTEM.md) now owns the
theme, token, typography, spacing, accessibility, CSS, and extension contract
that those pages must use. Route, localization, state, and security contracts
are accepted now; the project must not claim an end-to-end hosted journey
until those pages exist. Human prose lives under `server/i18n`; presentation
and email templates live under `server/templates`.

The server-owned browser runtime is now established as a root Vite module. A
release build generates its API types from the authoritative OpenAPI document,
compiles one immutable distribution, records the exact server version and
commit in that distribution, and packages it beside the Go executable. Server
startup rejects a missing or mixed-version distribution. The root HTTP module
serves only the declared hosted routes and existing fingerprinted assets;
API, health, the undeclared origin root, non-fingerprinted assets, and unknown
server paths retain their existing transport behavior. The browser package
also owns a generated, contrast-audited light/dark semantic-token adapter and
locally bundled typography. These are delivery and presentation foundations,
not the deferred visual page implementation.

Credential-bearing pages use no-store responses, strict Content Security
Policy, `Referrer-Policy: no-referrer`, frame denial, MIME-sniffing protection,
host-only secure cookies, CSRF protection, and no third-party script, font,
analytics, or image. Fragment credentials are captured and removed from
history immediately.

One `BrowserAuthenticationTransaction` owns the shared orchestration for web
login, desktop authorization, Invitation acceptance, and provider connection.
It stores hashed bearer proofs, exact purpose and expected provider, bounded
destination, creation/expiry/consumption state, and only the minimum resolved
identity needed for its terminal effect. A host-only Secure, HttpOnly,
SameSite=Lax cookie scoped to the authentication path carries an opaque browser
proof, never encoded User, Invitation, provider, purpose, or redirect state.
The ordinary hosted-login specialization, including state-bound provider
failure recovery and `/authorization/complete`, lives in the
[browser login reference](browser-login.md).

The application-facing Browser Authentication Store is purpose-specific. Its
named operations return only the facts required for the next decision: a new
identity and expiry, an Invitation identity and claim hash, an approved
callback and code expiry, or the created Session with authoritative credential
expiries. They never return a mutable whole transaction for application code to
transition. PostgreSQL acquires the necessary locks, obtains one authoritative
timestamp for each transition, computes deadlines, and destroys consumed or
cancelled proofs in the same atomic operation. Application code validates the
narrow result and cannot reconstruct transition state from a broad aggregate.

The hosted `/account/connect-provider` orchestration remains part of the later
server-page phase. Until that page exists, the implemented API starts the
provider-protocol leg directly: `ExternalLoginState` carries a closed
`connect` purpose, the exact current User, and the durable audit attempt across
the provider redirect. It can complete only by attaching the proved immutable
subject to that User and never creates a Session. This is not a second hosted
browser-orchestration model; the future page must wrap the same terminal
contract in `BrowserAuthenticationTransaction`. The application supplies only
the validated bounded state lifetime; one PostgreSQL timestamp establishes
creation and expiry, and callback consumption uses PostgreSQL time rather than
the initiating or callback node clock.

The terminal purpose is closed:

- ordinary web login creates a Web Session;
- desktop authorization resolves a User and issues one code;
- Invitation acceptance creates or resolves the User and applies its package;
- provider connection attaches an identity to the already authenticated User.

Invitation acceptance and desktop authentication do not create an unexpected
persistent browser Session. An existing valid browser Session may approve a
desktop transaction after an explicit account/device confirmation and any
required recent or strong authentication.

For local Invitation acceptance, the `/join` bootstrap removes the fragment
credential before rendering and exposes it only as purpose-specific in-memory
state. The browser-support API exchanges that claim once for a five-minute
public handle and a separate host-only HttpOnly browser proof. PostgreSQL
locks and rechecks the Invitation, computes the deadline from one authoritative
database timestamp, and stores only their hashes plus the exact Invitation and
its claim hash in the same named creation aggregate.
Account-creating and existing-Session acceptance use distinct terminal
operations; completion clears every transaction proof. The server never
returns the original claim after exchange or places it in a query string,
provider state, log, or audit field.

Provider callbacks, local-password validation, and current account state prove
authentication but do not authorize a terminal effect. Policy, Invitation, and
account state are rechecked at commit. Public failures use bounded categories
and never distinguish unknown account, wrong Invitation, disabled User, or
unlinked subject unnecessarily.

## Desktop authorization

Electron is a native public client. Proctor uses the system browser and a
narrow Proctor Desktop Authorization protocol rather than an embedded login
window or a general OAuth authorization-server product. The flow follows the
native-app requirements in [RFC 8252](https://www.rfc-editor.org/rfc/rfc8252)
and the current OAuth security practices in
[RFC 9700](https://www.rfc-editor.org/rfc/rfc9700):

1. the desktop validates discovery, generates high-entropy state and an S256
   PKCE verifier/challenge, starts a loopback listener, and creates a server
   authorization transaction;
2. the server returns one exact permitted hosted authorization URL;
3. the system browser completes local or external authentication against the
   pinned installation;
4. the server redirects only a short-lived opaque code and state to the exact
   loopback URI;
5. the desktop posts code and verifier to the pinned server origin (HTTPS in
   production; loopback HTTP only in explicit local development);
6. the server atomically consumes the code and creates one ordinary Desktop
   Session with rotating access and refresh credentials.

Initially accepted callbacks are exact IP-literal loopback URLs using an
ephemeral port and random path: `127.0.0.1` or `[::1]`, never `localhost`.
Non-loopback hosts, credentials, fragments, arbitrary schemes, remote ports,
and caller-selected commands are rejected. A custom scheme or claimed HTTPS
link requires a separate review.

The persisted authorization transaction contains only hashed transaction
handle/state and code, S256 challenge, exact callback, pinned installation,
bounded client/device metadata, and safe lifecycle state. It never contains
the verifier, provider tokens, Session credentials, or raw callback data.
Browser authentication may live for approximately five minutes; the final
code is single-use and approximately one minute. PostgreSQL time is
authoritative. Application nodes pass bounded lifetimes rather than absolute
deadlines into the named Store operations; one PostgreSQL timestamp establishes
transaction creation/expiry, code expiry, Session idle/absolute expiry, and
access/refresh credential expiry for each atomic transition.

Concurrent attempts are independent. Completing, canceling, or redeeming one
does not invalidate another device or browser transaction. An ambiguous or
replayed exchange creates no additional Session. Any node may continue the
flow; PostgreSQL, not sticky routing or process memory, owns transaction state.

The server protocol is exposed through four `no-store` operations:
`POST /api/v1/auth/desktop/authorizations` starts the transaction,
`POST /api/v1/auth/desktop/authorizations/approve` requires an existing Web
Session and approves the pinned authentication path,
`POST /api/v1/auth/desktop/authorizations/cancel` proves and cancels a pending
transaction, and `POST /api/v1/auth/desktop/token` performs the one-use code
exchange. The authorization URL names the future `/authorize/desktop` hosted
page; implementing that page and the Desktop client remains outside the server
protocol slice.

Ordinary local or external browser login creates only a Web Session. Its
legacy provider-login request defaults to Web and rejects an explicit Desktop
client at both initiation and callback. Consequently every Desktop Session is
created only by the purpose-bound authorization transaction and PKCE/code
exchange above; provider-connection purposes remain distinct and never acquire
Desktop Session issuance as a side effect.

Access credentials remain memory-only in the desktop. Its privileged main
process stores only the rotating refresh secret in an approved OS-backed
credential store. `device_id` and `device_name` remain untrusted Session
metadata, not authentication or MFA bypass. When secure persistence is
unavailable, sign-in is session-only. There is no first-slice Device Token or
trusted-device aggregate.

## Local and external credential lifecycle

Disabling local login preserves password hashes but prevents login, recovery,
and new credential enrollment. Re-enabling makes retained valid credentials
usable again; it never creates credentials for external-only Users. Password
reset returns its ordinary generic accepted response but issues no token or
mail when local login is disabled or the account lacks a password.

An externally authenticated User may add a password only when policy permits
local credential enrollment, after strong recent authentication and verified
email control. Removing a password or unlinking an identity requires strong
recent authentication and another currently usable method. Removing a method
revokes Sessions authenticated through it and invalidates applicable recovery
state. No transition may strand the last active system administrator.

One User may link several external identities and retain a local password.
Linking is explicit and proof-bearing: an authenticated User starts a recent
Connect-provider transaction, or a valid Invitation authorizes the link to its
intended User. Provider-subject uniqueness decides concurrent races. Email
matching alone never initiates or completes a link, and administrators never
enter raw subjects.

If ordinary external login finds a profile email already used by an unlinked
User, it returns a safe account conflict. The person signs in through an
existing method and connects the provider, or an administrator issues a
reconciliation Invitation to the canonical account. The first access phase
does not implement general User merge because exam work, submissions,
authorship, audit, and credential conflicts require a separate
resource-by-resource consolidation design.

Provider login continues to resolve by immutable subject when email or profile
claims change. First provisioning may populate safe fields; later logins do not
silently overwrite the Proctor profile. Directory-managed profile fields,
relationship reconciliation, and provider-driven deprovisioning are deferred
to a separately reported policy. Missing or disabled provider accounts prevent
that authentication attempt without silently disabling the Proctor User.

## Invitation aggregate

Every Invitation has one immutable purpose and package:

- `student_class` establishes a student affiliation when needed and membership
  in one exact Class;
- `teacher_academic_unit` establishes a teacher affiliation when needed,
  Academic Unit membership, and one selected scoped Role Binding;
- `academic_unit_role` establishes another approved Academic Unit role package;
- `institution_role` establishes an approved Institution role package.

There is no empty-account Invitation. The inviter must be authorized for every
effect at issue time. A unit-scoped administrator cannot create a broader
package or delegate an action they do not possess.

Invitation state is `pending`, `accepted`, `revoked`, `expired`, or
`superseded`. It freezes normalized recipient email, purpose, targets, role,
effective bounds, optional locale and profile suggestions, inviter, and
authorization scope. A 256-bit random secret authenticates the claim; only its
domain-separated hash is stored. Default lifetime is seven days under bounded
Institution policy.

Resending an unchanged Invitation rotates the secret and invalidates every old
link. Changing recipient, purpose, target, role, or effective dates creates a
replacement and supersedes the old Invitation. Revocation is per Invitation.
Several compatible Invitations may share an email; pending student Class
Invitations conflict within the same Academic Period unless an explicit
transfer purpose supersedes the prior one.

Possession of the emailed secret proves access to the invited mailbox. It is
submitted from a URL fragment to the hosted page and never placed into CAS or
OIDC `state`. Before an external redirect the server creates a short-lived
single-use Invitation-claim transaction bound to browser proof, Invitation,
provider, and external state. An opaque handle crosses redirects. A provider
email match is useful but not required; a different verified provider email
already owned by another User is a conflict.

Acceptance rechecks PostgreSQL time, current Invitation and Access Policy,
inviter activity and present authorization, target/role validity, account
state, and academic invariants. Failure does not consume the Invitation and
may place it into administrative review without disclosing the exact reason to
the recipient. Expiry during authentication prevents final acceptance.

One named aggregate transaction creates or resolves the User, attaches the
credential or external identity when that purpose requires one, verifies the
invitation email, initializes User Settings when creating a User, applies the
purpose-specific relationship and Role Binding package, consumes the
Invitation, records audit, and prepares required mail. It either commits all
effects or none. No disabled placeholder User exists before acceptance.
Existing Users receive only missing effects; an already satisfied package
accepts idempotently and never changes canonical email.

Academic Unit and Institution Role Invitations are existing-User packages.
The authenticated User still presents the Invitation claim to prove control of
the invited mailbox; email equality alone never selects or changes an account.
Acceptance creates only the missing compatible Role Binding, or reuses the
already-satisfied binding, and creates no Affiliation, Academic Unit
membership, credential, Session, welcome message, or acceptance message. The
Institution form is privileged administration: issue requires a strong,
recent interactive Session and cannot be performed through a Personal Access
Token. Both forms freeze the exact Role, canonical action snapshot, scope, and
effective bounds, and recheck current inviter authority and target validity in
the committing PostgreSQL transaction.

Intended future dates are allowed, but acceptance never grants authority
retroactively: the effective start is the later of intended start and
acceptance. An intended end already elapsed cannot be accepted. Backdated
correction is a separate privileged operation.

Invitations are required for pre-User onboarding and may express consent, but
an authorized administrator may directly enroll, transfer, or assign an
already active User. Existing student progression uses a dedicated batch rather
than repeated account Invitations. Teacher packages retain provenance so ending
the package membership atomically ends Role Bindings originating from it;
independent bindings remain independent. Ending one relationship never
implicitly ends the User's broader Affiliation.

## Invitation administration and delivery

Individual administration provides create, bounded list/get, revoke, resend,
replacement, and safe acceptance/delivery inspection. Invitation records are
immutable apart from lifecycle transitions. Safe projections may contain
recipient email, purpose, target summary, role name, inviter, accepted User ID,
timestamps, and bounded delivery state; they never contain raw or hashed
secret, rendered mail, provider subject, claims, or transport internals.

Invitation and mail lifecycles are independent. Pending does not mean delivered
and SMTP Accepted does not prove inbox receipt. Creation atomically inserts the
Invitation and durable mail intent. Revocation immediately invalidates the
secret and cancels queued delivery where possible; already-sent mail becomes
harmless because acceptance reads authoritative state. Explicit resend creates
a new secret, occurrence, and deadline. No production API returns a raw link.

Invitation-required policy therefore needs configured durable mail. Automatic
reminders are outside the initial scope; explicit resend avoids retaining a
recoverable Invitation secret merely for reminders. Terminal Invitation records
retain operational recipient detail for 90 days by default before bounded
purge; security audit follows separate retention.

## Administrative batches and CSV

The access phase supports typed batch use cases rather than an arbitrary
operation interpreter:

- Invitation create, resend, and revoke;
- Affiliation add and end;
- Academic Unit membership add and end;
- Class enroll, end, transfer, and progression;
- Role Binding create and end;
- User enable and disable;
- selected-User Session revocation.

Bulk merge, external-identity attachment, password assignment, MFA removal,
and permanent deletion remain individual proof-bearing operations. Bounded JSON
batches accept up to 200 items and return per-item outcomes. A required batch
idempotency identity and one required bounded, request-unique key per item are
domain-separated by operation; repeated item keys invalidate every affected
row before execution. Each successful item retains a minimal secret-free outcome
atomically with its ordinary mutation. Within a duplicate group the smallest
stable item key is canonical and every other duplicate disposition is retained,
so reconnect recovery or row reordering cannot duplicate an Invitation or
delivery occurrence and changed retained-key reuse conflicts at the affected
item. Exact retained outcomes resolve before fresh claim or mail preparation.
CSV serves larger file-driven work.

Class-scoped student and Academic-Unit-scoped teacher imports select their
target outside the file. Institution-wide onboarding may carry a per-row kind
and canonical target names. Common CSV requires `email` and may include
`username`, `display_name`, `first_name`, `last_name`, `locale`, and `timezone`.
Institution-wide student targets use exact Academic Period, Programme,
Programme Level, and Class canonical names; teacher and role targets use exact
Academic Unit and role names. Unknown columns are ignored as requested but
their header names appear once in preview so misspellings are visible.

Existing-User academic administration CSV selects one exact Institution,
Academic Unit, or Class scope outside the file. Each row names one operation
from the same closed JSON union plus a stable reference and only its applicable
`user_id`, `relationship_id`, `role_id`, `affiliation_kind`, `start_at`, and
`end_at` fields. It never accepts identity attachment, credential intervention,
MFA removal, deletion, an arbitrary command name, or a per-row scope escape.
Preview freezes the resolved User/relationship, target revision, Role revision,
and effective bounds before commit.

Initial immutable limits are 10 MiB, 50,000 data rows, UTF-8 with optional BOM,
RFC 4180-compatible quoting, unique bounded headers and fields, and no NUL or
invalid Unicode. Original CSV bytes are deleted after normalized preview
creation.

The workflow is upload, asynchronous parse and complete validation, immutable
content-digested preview, explicit commit, durable execution Job, and
downloadable final report. Preview resolves canonical names to IDs/revisions
without side effects. Commit names the exact preview and one of
`require_all_valid` or `valid_rows_only`; the latter skips invalid preview rows.
Targets and current authorization are still revalidated during execution.

Each row is one named atomic aggregate transaction and rows remain independent.
Runtime conflicts yield `completed_with_errors` rather than rolling back prior
rows. Duplicate email/purpose/target rows are preview errors. Existing pending
or already-satisfied effects are explicit no-ops. Student destination changes
within one Period require an explicit transfer operation. Commit is idempotent
and one preview creates at most one execution Job.

Submission and each row reauthorize. Losing authority stops later unauthorized
rows; a Job never carries a reusable authorization receipt. One mutating batch
of the same type executes against one target scope at a time. Cancellation
stops new rows, lets an already committing row finish, and never rolls back
committed work. Compensation uses explicit inverse use cases.

Detailed previews and reports retain for seven days. Reports preserve source
row, safe reference, normalized operation, status, created IDs, and public
error code; downloads reauthorize and escape spreadsheet formulas. Audit,
logs, metrics, and generic Job projections contain only safe IDs, counts,
timing, and closed codes, never recipient lists or rows.

Student progression names exact source/destination Periods and Classes, dry
runs every student conflict, preserves historical membership, and commits each
student independently. Same-Period transfer ends and replaces enrollment
atomically; progression into another Period creates the new history without
rewriting the old.

## Authorization and assurance

The detailed role, action, inheritance, and visibility contract lives in
[authorization](../../authorization-audit/references/authorization.md). Access administration introduces distinct
grantable actions for Access Policy, Invitations, onboarding batches, external
identity reconciliation, granular academic resources, and Role Binding. Generic
`job.manage` never authorizes onboarding.

PAT automation may perform ordinary student/teacher onboarding only when both
the PAT ceiling and current scoped Role Binding authorize every effect. Access
Policy, identity reconciliation, administrative Role Bindings, credential
intervention, and sensitive batches require a strong recent interactive
Session. Credential type is checked per row.

Authorization is checked before avoidable inspection and again at terminal
mutation. Scope-constrained lists execute in persistence. Safe audits record
actor, action, target and authorization scope, revisions, counts, and outcome;
they exclude Invitation secrets, provider subjects, claims, credentials,
complete emails/rows, and rendered mail.

Authentication, Invitation, and desktop-authorization attempt limits reuse the
private shared attempt accounting with domain-separated digests for source,
transaction, provider, Invitation, identity, and exchange. Desktop Start and
exchange use distinct domains and account before durable transaction or audit
work. Counters are applied before combined decisions and fail closed. Raw
identity and credential material never enters cache keys or diagnostics.

## Mail and hosted-page dependencies

The onboarding additions to the closed transactional-mail catalog and their
single-message semantics live in
the [`transactional-mail` skill](../../transactional-mail/SKILL.md). Local
credential mail is suppressed when no local credential can be used; disabling
local login never globally suppresses Invitations or applicable external-user
security notices. Provider-verified email avoids redundant verification.

Invitation delivery retries only while the Invitation remains pending and
before expiry. Acceptance, revocation, supersession, or expiry suppresses
unsent delivery and destroys its encrypted payload. The initial product sends
no automatic reminder.

The accepted access design does not depend on mail implementation, but
invitation-required admission cannot become operational until durable mail
intent can commit atomically with Invitation creation. Hosted-page visual work
now depends on the established webapp design-system contract and still
requires page and component implementation.

## Operations, retention, and verification

PostgreSQL and the Job engine are critical dependencies. SMTP outage or an
onboarding backlog produces degraded subsystem status without failing general
HTTP readiness. Metrics expose bounded queue, age, latency, outcome, retry, and
scope counts. Logs contain safe transaction, Invitation, batch, Job, User, and
target IDs plus closed codes only.

Browser-authentication secrets are destroyed on terminal completion. Pending
and code-issued transactions become an expired terminal state at their
authoritative PostgreSQL deadline, destroying their remaining proofs. Every
terminal lifecycle state retains only safe transaction metadata for 24 hours
for idempotency and diagnostics, then is purged. A non-durable periodic runtime
task on every node processes only bounded PostgreSQL pages; row claiming makes
concurrent passes converge safely without Job, Attempt, occurrence, or
permanent-deduplication-ledger rows. Expiry and retention therefore progress
even when no authorization request writes occur, while public mutations never
run an unbounded cleanup scan. Public failures avoid account
and policy enumeration. Durable audit records successful
Session issuance, desktop authorization, Invitation acceptance, identity link
changes, policy denial after authentication, and administrative reconciliation;
ordinary invalid public traffic remains counters and bounded diagnostics rather
than attacker-controlled audit volume.

Provider-connection state follows the same bounded runtime-maintenance model.
An expired abandoned `connect` state terminalizes its existing critical audit
attempt as failed before safe state metadata is purged after 24 hours. Provider
rejection, invalid assertion, and any post-consumption failure terminalize that
same attempt synchronously; they never leave a false in-progress audit.

Verification includes Store conformance and real PostgreSQL integration for
bootstrap atomicity, Access Policy fencing, provider removal/re-enable,
lockout prevention, callback and code replay, mix-up rejection, loopback URI
validation, PKCE, multi-node continuation, ambiguous exchange, Invitation
races and acceptance, scope filtering, role delegation ceilings, CSV parsing
and formula escaping, Job cancellation/recovery, mail atomicity, disabled mail,
secret absence, and terminal cleanup. HTTP/OpenAPI agreement and route-assurance
tests cover every public and administrative operation.

## Delivery order and reference evidence

Implementation proceeds as independently reviewable vertical slices:

1. scoped Academic Period ownership, granular academic resources/actions, and
   existing-installation `system_admin` action reconciliation;
2. Access Policy, bootstrap secret, discovery, and dynamic administration;
3. purpose-aware browser authentication and desktop authorization exchange;
4. credential linking and lifecycle;
5. Invitation persistence and acceptance;
6. transactional-mail integration and terminal external-identity Invitation
   reconciliation;
7. typed administrative batches, CSV onboarding, and progression;
8. server-hosted page and component implementation on the established design
   system.

Mattermost was inspected as behavioral evidence at revision
`8ce3c54a5ed76b2aa39a46cf8a1b517ea53ec0cc`, principally its user, team
Invitation, OAuth, desktop-login, template, upload, import, and Job paths.
Proctor does not copy its single-auth-method User model, first-user promotion,
plaintext or query-string tokens, email-based account attachment, non-atomic
Invitation consumption, synchronous send, or unscoped recipient audit. Any
direct or substantial adaptation must identify exact upstream paths and comply
with the repository provenance and license rules.
