# Identity and authentication

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
[Access and onboarding](./access-and-onboarding.md).

## Application ownership

Identity follows the focused-service and public-surface rules in
[Application](./application.md#interfaces-and-public-surface). Its application
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
reauthentication, a composed assurance requirement, or a refresh credential.
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
[Access and onboarding](./access-and-onboarding.md#desktop-authorization).

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

## MFA

The principal records authentication strength and completion time. Sensitive
operations can require strong and/or recent authentication. TOTP secrets are
stored in versioned AES-256-GCM `secretseal` envelopes authenticated to the
fixed `mfa.totp` purpose and owning User. The MFA key ring is independent from
mail, Memberlist, and every other cryptographic domain. Challenges are
replay-protected, recovery codes are hashed and single-use, and recovery-code
values are shown only once. External-provider MFA counts only when an
explicitly configured trusted assertion proves it.

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
Invitation claim may instead admit a new relationship-free User and exact
identity link; it does not itself accept the Invitation or attach that identity
to another existing User.
Provider profile changes do not silently overwrite established Proctor fields.
Provider-driven profile synchronization, relationship reconciliation, and
deprovisioning require a separately reported policy; a failed provider account
does not silently disable the Proctor User.

### CAS

The durable key is `(provider ID, opaque subject)`, where the authoritative
subject is explicitly mapped from `<cas:user>` or a released attribute. The
callback consumes state once and validates the ticket through the back channel
against the exact service URL. CAS success or `renew=true` does not prove MFA
without a configured trusted assertion. Proxy tickets, gateway login, CAS
single logout, and implicit multi-institution routing are outside the current
contract.

### OIDC

OIDC uses exact issuer discovery and Authorization Code with S256 PKCE. The
browser proof is the PKCE verifier and a domain-separated digest forms the
nonce. The ID token signature, issuer, audience, expiry, nonce, and any
included `at_hash` are verified. User-info `sub` must match the ID-token `sub`
and cannot override authentication time or MFA claims. Provider tokens, codes,
and raw claims are ephemeral and never persisted, returned, logged, or
audited.

A future cross-site SAML POST flow requires a reviewed two-stage design that
retains the validated response and completes on a same-origin GET. The global
browser cookie policy is not weakened to accommodate it.
