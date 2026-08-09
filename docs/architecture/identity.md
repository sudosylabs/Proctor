# Identity and authentication

Identity is a Proctor server domain. It owns accounts and affiliations, local
credentials, external identity links, login policy, sessions, refresh
credentials, personal access tokens, purpose-specific account tokens, MFA, and
security-related audit events.

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

## Installation bootstrap

Bootstrap is an explicit one-time aggregate, never a first-user side effect.
A PostgreSQL-serialized transaction requires a pristine installation and
creates the institution, first local administrator, encoded password,
protected `system_admin` role, institution binding, installation marker, and
successful audit event atomically. Losing or failed attempts leave no partial
state and bootstrap never mints a special session.

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

## Sessions and browser transport

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

This contract assumes the Electron renderer uses the installation origin. A
different-origin renderer requires a reviewed main-process or cross-origin
handoff; SameSite and CORS protections are not weakened opportunistically.

## CLI and purpose-specific credentials

CLI automation uses personal access tokens rather than long-lived sessions.
Interactive CLI login should use device authorization or a browser callback
when supported. Access and refresh credentials are never accepted from URL
query parameters.

Password-reset and email-verification tokens bind to the normalized account
email at issuance. Reissuance invalidates the prior active token, browser links
carry raw credentials in a fragment, and completion consumes the token
transactionally. Password-reset requests return a generic accepted response;
successful completion changes the password, revokes all sessions, and records
the terminal audit atomically.

## MFA

The principal records authentication strength and completion time. Sensitive
operations can require strong and/or recent authentication. TOTP secrets are
encrypted, challenges are replay-protected, recovery codes are hashed and
single-use, and recovery-code values are shown only once. External-provider
MFA counts only when an explicitly configured trusted assertion proves it.

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
providers create ordinary Proctor sessions. Auto-provisioning never links an
existing account because email or username matches, and released affiliation
claims never create roles or memberships.

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
