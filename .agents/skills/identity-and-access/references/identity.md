# Identity and authentication reference

Identity is a Proctor server domain. It owns accounts and account state, local
credentials, external identity links, login policy, sessions, refresh
credentials, personal access tokens, purpose-specific account tokens, MFA, and
security-related audit events. Academic administration owns affiliation
lifecycle; identity and access policy may consult those relationships through
narrow application or persistence contracts.

## Identity model

- `User` contains profile and account state, not credentials, provider
  subjects, affiliations, roles, or permissions.
- `ExternalIdentity` links a user to the opaque subject of a stable provider
  ID; email and username are never identity keys.
- `PasswordCredential` stores only an established password hasher's encoded
  output and is absent for external-only accounts.
- `Affiliation` is non-exclusive and effective-dated.
- `AcademicUnitMember` and `ClassMember` record relationships; staff access is
  granted through scoped roles.
- `Session` stores authentication context but no bearer credential or role
  snapshot. `SessionCredential` stores access/refresh hashes and rotation
  lineage.
- `PersonalAccessToken` is finite, hashed, revocable, explicitly action-scoped,
  and optionally constrained to an academic-unit subtree.
- `UserToken` is purpose-specific, hashed, expiring, and single-use.
- `Invitation` is a separate durable pre-User aggregate; account-purpose tokens
  never double as invitations.
- `AccessPolicy` is the revisioned application authority for available login,
  credential-enrollment, provider-admission, invitation, and desktop handoff
  capabilities.

The complete accepted access, hosted-browser, desktop authorization,
Invitation, and batch-onboarding contract is
[access and onboarding](access-and-onboarding.md).

## Application ownership

Identity follows the focused-service and public-surface rules in the
[`server-boundaries` application reference](../../server-boundaries/references/application.md#interfaces-and-public-surface). Its application
policy is organized around authentication and session issuance, external
authentication, MFA, account recovery and verification, Personal Access
Tokens, self-service session management, and account-state administration
rather than one large Identity service.

Each service receives the exact existing per-model or named aggregate store
contracts it needs. Identity does not introduce a generic repository, retain
the root `store.Store`, or decompose atomic user creation, credential rotation,
token consumption, MFA, or revocation operations into application-managed CRUD
sequences. Clocks, secure generators, hashing, mail, rate limiting, provider
registries, and diagnostics remain narrow explicit dependencies rather than a
utility or environment object.

Authentication attempt accounting is one private application module shared by
local login, account recovery, external-authentication initiation, and
installation bootstrap. It owns bounded source normalization, domain-separated
hashed cache keys, sequential counter mechanics, sliding windows, and threshold
evaluation. Each use case retains which identity, source, operation, or
provider dimensions apply, its public error mapping, and any successful-action
reset policy. The module is constructed once over the disposable cache and is
not a cache locator or a public application capability.

Every applicable counter is incremented before the combined decision. Counter
increments are atomic per key but not across keys; a partial cache failure
fails the request closed and does not roll back an earlier disposable
increment. A successful local login resets only its combined identity/source
counter, while the source-wide counter remains. Raw identities, sources,
operations, and provider qualifiers never appear in cache keys, errors, logs,
or diagnostics.

Durable audit remains a cross-cutting application capability exposed through
narrow consumer-owned ports. Critical success audit stays inside the named
atomic store operation where required. Cache invalidation, realtime
publication, and cluster fan-out occur only after durable commit through
service-specific effect ports. A separately constructed authentication-cache
invalidation capability is shared with Realtime so neither Authentication nor
Realtime requires mutable callback wiring or ownership of the other service.
Application composition constructs that invalidator and Realtime as sibling
capabilities before projecting only the required effects into each Identity
service.

Focused services collaborate through consumer-owned behavioral capabilities,
such as session issuance, MFA verification, Personal Access Token bearer
resolution, user provisioning, password transition, and session revocation.
They do not retain sibling implementations or a service aggregate. Observable
authentication behavior, error precedence, audit ordering, credential
ceilings, and post-commit effects remain characterization-locked; an
intentional correction is reviewed separately.

`app.New` constructs the fixed set of focused Identity services in explicit
dependency order and projects each exact Store or behavioral capability at its
constructor boundary. Runtime services neither receive nor query a composition
aggregate.
Authentication owns credential validation, principal establishment, ordinary
session issuance, refresh rotation, and logout. External authentication uses a
narrow session-issuer capability; Personal Access Token resolution and MFA
verification remain separately owned capabilities. Account verification and
password recovery share a focused purpose-specific token service while
retaining distinct named use cases and atomic terminal transitions. MFA
cryptographic mechanics remain separate from MFA enrollment, challenge,
recovery-code, and assurance-transition policy.

External authentication receives Desktop Authorization as a required behavioral
interface at construction. The Desktop Authorization module resolves the current
bound transaction from the browser proof and prepares its return path; the
verified provider callback authenticates only that exact transaction. The public
facade delegates this collaboration without traversing Desktop persistence or
assigning dependencies after construction. Ordinary Web Session issuance,
provider connection, Invitation admission, and Desktop code exchange retain
their distinct purposes and owners.

Focused service constructors validate all required contracts and remain inert.
Post-commit cache or Realtime failure does not rewrite a successfully committed
durable result into a transaction failure; it produces bounded diagnostics and
relies on authoritative reconstruction or revalidation. Internal errors retain
only the detail needed for policy and safe diagnostics, while facade errors
preserve enumeration resistance and exclude credentials, tokens, provider
assertions, recovery secrets, and unnecessary personal data from logs and
audit.

Self-service session management and administrative control of another user's
sessions remain separate authorization policies even when they share session
transition contracts. Personal Access Token ownership likewise exposes a
narrow bearer resolver to authentication separately from its administration
use cases.

## Installation bootstrap

Bootstrap is an explicit one-time aggregate, never a first-user side effect.
A PostgreSQL-serialized transaction requires a pristine installation and a
high-entropy deployment-owned one-time secret. It creates the Institution,
first local administrator, encoded password, protected `system_admin` role,
Institution binding, Access Policy revision 1, installation marker, successful
audit, and secret consumption atomically. Losing or failed attempts leave no
partial state and bootstrap never mints a special Session. The administrator's
email begins unverified because deployment authority does not prove mailbox
control.

Every installation begins with this local administrator. External-only is a
post-bootstrap operating policy: an administrator first verifies email, tests
mail and a configured provider, links their provider identity, and only then
may disable local login. Current-state invariants prevent removal of the last
usable system-administrator authentication path. Host-level emergency recovery
is an offline audited operation, not a network endpoint or hidden local-login
bypass.

Built-in roles are server-owned. System-administrator bindings exist only at
institution scope, and ending one is serialized so another active binding
remains. Adding an action to the closed registry requires reconciliation of
the built-in role in the same release.

## Route authentication

Every route explicitly requires one of: public access, an authenticated
principal, an interactive session, strong/MFA assurance, recent
reauthentication, a composed assurance requirement, a bounded MFA-recovery
Session, or a refresh credential.
Administrative privilege is an application authorization decision, not a
transport authentication class.

The request principal is immutable and contains security-relevant identity,
credential, provider, assurance, client, and authentication-time context. It
does not snapshot roles, permissions, or academic memberships. Route matrix
tests reject unclassified handlers.

## Sessions, browser transport, and desktop handoff

Interactive sessions use random opaque access and rotating refresh
credentials whose hashes alone are persisted. Idle and absolute expiry are
separate; activity writes are debounced; concurrency is bounded; users can
list and revoke sessions; account and credential security changes can revoke
all sessions. Authorization always resolves current role bindings.

Self-service Session revocation and logout are critical audited mutations.
The immutable principal selects the owning User; revoking a listed Session
also verifies its current ownership. These intrinsic self-service operations
do not grant administrative `session.manage` authority over another User.
Their audit uses `session.manage` with the distinct `revoke_own_session`,
`revoke_own_sessions`, or `logout` operation and only bounded Session/User
identifiers plus approved Session result data. A durable attempt precedes the
mutation, and Session/credential-family revocation commits atomically with its
successful completion. An unavailable required audit fails closed before
post-commit cache or realtime effects. Self-service reasons never create an
administrator security notice.

Repeating a single listed-Session revocation returns concealed not-found once
that Session is revoked, with a failed mutation attempt when ownership was
resolved. Logout on an already absent or revoked caller Session remains a
successful audited no-op. Revoking all own Sessions with none remaining also
completes a successful audit with a zero count. These no-ops produce no new
revocation effects. Transport authentication still rejects a credential after
its committed revocation; no-op behavior applies to an already-established
application invocation, including concurrent requests.

Every new Session authentication resolves its current credential, Session, and
active User through authoritative Store reads and fails closed if a required
read fails. Positive authentication snapshots cannot grant access after a
committed revocation or account disablement. Activity debounce and best-effort
connection closure remain transient effects. The precise post-commit guarantee and the
limits for in-flight requests and established WebSockets are defined in
[Cluster delivery guarantees](../../../../server/cluster/GUARANTEES.md#authoritative-session-authentication).

Credential expiry decisions use one UTC instant at PostgreSQL microsecond
precision through the application and Store. Session access, refresh rotation,
activity updates, Personal Access Token resolution, pending MFA activation,
and MFA Session upgrades reject at the deadline itself. Convert legacy wire,
notice, and audit timestamps at their owning projection; they must not round
the instant used for a credential validity decision. TOTP time steps remain
protocol counters rather than domain timestamps.

Electron/web sessions use host-only HttpOnly cookies. Production cookies are
Secure and SameSite=Lax; the refresh cookie is scoped to its endpoint. Unsafe
cookie-authenticated requests use a rotating signed double-submit CSRF token.
Refresh rotates access, refresh, and CSRF credentials. Mixed bearer/cookie
sources and duplicate credential cookies are rejected rather than resolved by
precedence.

Proctor Desktop does not render authentication pages or reuse browser cookies.
It discovers the installation, opens server-hosted authentication in the
system browser, and receives a purpose-built native-public-client handoff. The
server validates an exact IP-literal loopback callback, high-entropy state, a
short-lived single-use code, and an S256 PKCE verifier before issuing one
ordinary Desktop Session. Access and refresh credentials never appear in URLs;
provider credentials and tokens terminate at the server. Exact discovery,
transaction, storage, concurrency, and callback rules live in
[access and onboarding](access-and-onboarding.md#desktop-authorization).

## CLI and purpose-specific credentials

CLI automation uses personal access tokens rather than long-lived sessions.
Interactive CLI login should use device authorization or a browser callback
when supported. Access and refresh credentials are never accepted from URL
query parameters.

Password-reset and email-verification tokens bind to the normalized account
email at issuance. Reissuance invalidates the prior active token, browser links
carry raw credentials in a fragment, and completion consumes the token
transactionally. Issuance atomically persists the token hash, successful audit,
encrypted frozen message, occurrence, and reserved credential-delivery Job;
reissue also suppresses the prior unsent delivery. Password-reset requests
return a generic accepted response; successful completion atomically changes
the password, revokes all sessions, consumes the token, records the terminal
audit, and queues only the password-changed security notice.

Email changes preserve prepared User lifecycle history, including imported
timestamps ahead of the database clock. User updates and superseded token
archival cannot move existing lifecycle metadata backwards. Those metadata
floors never determine credential validity: one PostgreSQL instant establishes
the replacement token and frozen mail lifetimes, and superseded credentials
become unusable atomically regardless of their archival timestamp.

Password proof names the verified Password Credential and its revision. Every
password reset or offline password rotation advances that revision, including
replacement with the same password. A work-factor rehash changes only the
encoded hash and update time, conditional on the credential, revision, and hash
that were verified; it never changes the password-change time. A competing
credential write fails the rehash with the ordinary generic login rejection.
Password hashing and credential generation occur before persistence locks.

Ordinary Session creation and every Desktop Authorization authentication,
approval, and exchange recheck current password proof. Password reset takes the
same per-User Session lock before locking the token, User, or credential rows.
If Session creation commits first, reset revokes that Session; if reset commits
first, the old proof cannot create a Session. Removal and re-enrollment also
invalidate old proof through the credential identity. A reset therefore rejects
unfinished authentication based on the earlier password, including pending
Desktop handoffs, while preserving each flow's generic public failure.

Reusing a Web Session for Desktop Authorization atomically rechecks that exact
Session and its access credential, including ownership, revocation, and expiry.
The Store derives authentication context from the current Session and captures
the current password proof when applicable; an earlier Principal snapshot does
not authorize a new handoff after reset. The proof remains private to the
durable handoff and never enters a browser projection.

Desktop authentication uses the PostgreSQL transition instant for fresh local
password proof. External authentication retains older asserted authentication
and MFA instants; future assertions are capped at that transition instant.
Session reuse preserves the exact persisted authentication and MFA instants
without refreshing assurance or rounding to milliseconds. Inconsistent future
Session provenance fails atomically. The resulting handoff must satisfy its
persisted invariants before commit; authentication timestamps never extend
transaction or credential deadlines.

## MFA

The principal records authentication strength and completion time. Sensitive
operations can require strong and/or recent authentication. TOTP secrets are
stored in versioned AES-256-GCM `secretseal` envelopes authenticated to the
fixed `mfa.totp` purpose and owning User. The MFA key ring is independent from
mail, Memberlist, and every other cryptographic domain. Challenges are
replay-protected, recovery codes are hashed and single-use, and recovery-code
values are shown only once. External-provider MFA counts only when an
explicitly configured trusted assertion proves it.

Primary reauthentication preserves the Session's original method, provider,
initial authentication and MFA history. Password proof targets the exact current
User and current password revision. A separate optional reauthentication instant
refreshes recency; it does not manufacture a new MFA completion. Both primary
reauthentication and challenge commit their Session transition and required audit
atomically, then invalidate disposable credential state. Setup, activation,
recovery-code replacement, and disablement recheck the live current Session
under the per-User Session fence before the MFA fence. Activation and challenge
use a post-lock PostgreSQL instant. Factor attempts have bounded accounting.

Institution-assisted reset is a protected system-administrator action requiring
strong recent interactive authentication, an outside-Proctor identity-verification
attestation, and a bounded reason and verification reference. It targets another
active User, including another administrator, and never permits self-reset. The
last usable administrator primary path remains protected. The reset aggregate
retires local factors and codes, revokes Sessions and PATs, invalidates unfinished
credential grants, and commits a security-notice reservation and required audit
with a permanent per-User generation and reset instant.

A fresh primary proof after reset can issue only a restricted Web Session. Ordinary
principal validation rejects it. Only explicit status, fresh-proof, setup,
activation and logout paths accept this context. Normal challenge, PATs, Desktop
handoff and other account operations cannot restore access. Only activation of a
new local authenticator clears the restriction for the activating Session; other
restricted Sessions do not acquire ordinary authority. Generation and reset time
survive activation, so pre-reset password proofs and unknown-User provider/browser
transactions cannot regain authority afterwards. Required recovery persists when
MFA or the provider is disabled; online reset refuses when enrollment is unavailable.

An external-only User retains the exact existing provider identity. Ordinary SSO
resolves the account but cannot satisfy assisted recovery: a separate bound flow
requires verified fresh primary proof from that provider. No email match, Invitation
or alternate primary credential substitutes for this proof. Hosted fresh-proof
continuations are closed to MFA management and provider linking, return current
state, and require the final sensitive action explicitly. Restarting pending setup
replaces its old secret. A lost activation response is resolved through status and
explicit recovery-code regeneration using the new authenticator; once-shown codes
are never reconstructed.

## External provider boundary

Protocols live under `server/platform/externalauth` and implement a
protocol-neutral application contract. The composition root builds an
instance-scoped provider registry; configuration reload atomically swaps a
complete replacement set while in-flight requests retain their resolved
provider. Callback data is bounded and opaque outside the adapter. Network
clients use timeouts, bounded responses, and redirect rejection, and all
credentials, codes, tickets, tokens, response bodies, subjects, and raw claims
are redacted from logs and audits.

External login state is random, hashed, expiring, one-use, PostgreSQL-backed,
and bound to a separate host-only SameSite=Lax browser-proof cookie. Successful
provider authentication resolves one purpose-aware browser transaction. Web
login creates a Web Session; desktop authorization prepares a one-use code;
Invitation acceptance applies its package; provider connection links the
identity to an already authenticated User. Auto-provisioning never links an
existing account because email or username matches, and released affiliation
claims never create roles or memberships.

One User may link several external identities and may also retain a local
password when policy permits. Linking to an existing User requires current
proof from both the existing User context and provider transaction. A valid
Invitation admission flow instead accepts its exact package and binds the proved
provider identity to the User admitted by that Invitation. It creates no ordinary
Web Session and cannot substitute for assisted MFA recovery.
Provider profile changes do not silently overwrite established Proctor fields.
Provider-driven profile synchronization, relationship reconciliation, and
deprovisioning require a separately reported policy; a failed provider account
does not silently disable the Proctor User.

### CAS

The durable key is `(provider ID, opaque subject)`, where the authoritative
subject is explicitly mapped from `<cas:user>` or a released attribute. The
callback consumes state once and validates the ticket through the back channel
against the exact service URL. CAS success or `renew=true` does not prove MFA
without a configured trusted assertion. Fresh primary reauthentication sends
`renew=true` both at login and at service-ticket validation, as required by the
[CAS protocol](https://apereo.github.io/cas/development/protocol/CAS-Protocol-Specification.html).
The verified fresh proof uses the bound flow start as its conservative instant.
Proxy tickets, gateway login, CAS
single logout, and implicit multi-institution routing are outside the current
contract.

### OIDC

OIDC uses exact issuer discovery and Authorization Code with S256 PKCE. The
browser proof is the PKCE verifier and a domain-separated digest forms the
nonce. The ID token signature, issuer, audience, expiry, nonce, and any
included `at_hash` are verified. User-info `sub` must match the ID-token `sub`
and cannot override authentication time or MFA claims. Provider tokens, codes,
and raw claims are ephemeral and never persisted, returned, logged, or
audited. Fresh primary reauthentication requests `prompt=login` and `max_age=0`
and requires signed `auth_time` no earlier than the bound flow's start (at the
claim's second precision) and no later than the current instant. `iat` is never a
substitute for fresh `auth_time`; this follows
[OIDC Core](https://openid.net/specs/openid-connect-core-1_0.html).

A future cross-site SAML POST flow requires a reviewed two-stage design that
retains the validated response and completes on a same-origin GET. The global
browser cookie policy is not weakened to accommodate it.
