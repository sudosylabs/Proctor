# Browser login reference

This reference owns the accepted preimplementation design for ordinary
server-hosted browser login. It specializes the shared
`BrowserAuthenticationTransaction` architecture in
[access and onboarding](access-and-onboarding.md); that reference remains
authoritative for Access Policy, discovery, provider admission, identity
resolution, and Session invariants. Code, tests, and the OpenAPI document
remain authoritative for the currently implemented wire contract.

The routes in this reference are not implemented yet. They must enter the
runtime, OpenAPI sources, generated clients, and agreement tests as one
vertical protocol slice. Until then, the existing password-login and provider
routes remain the truthful public API and must not be silently documented as
the transaction protocol below.

## Purpose and boundary

Ordinary hosted login creates one Web Session through one exact authentication
path. It never accepts an Invitation, approves Proctor Desktop, connects a
provider to an existing User, registers a User, or chooses a caller-supplied
post-authentication destination.

The `web_login` purpose of `BrowserAuthenticationTransaction` owns the whole
browser journey:

1. select one local-password or configured-provider path;
2. bind that choice to a short-lived browser proof;
3. complete or reject authentication without changing purpose;
4. create at most one Web Session on success; and
5. expose one bounded terminal outcome to `/authorization/complete`.

Provider callbacks and password validation prove authentication facts. They do
not choose the terminal effect. The terminal Store operation rechecks current
policy and User state and is the only operation that may create the Web
Session.

The first product destination is fixed to `/authorization/complete`. Neither
`/login`, provider state, provider callbacks, nor the terminal page accepts a
free-form return or failure URL. A future authenticated destination requires a
new bounded destination field on this purpose, with an allow-list and its own
review.

## Transaction creation

The login page creates a transaction only after an explicit authentication
action. It sends one member of this closed request union to
`POST /api/v1/auth/browser/logins`:

```json
{ "authentication_path": "password" }
```

```json
{
  "authentication_path": "provider",
  "provider_id": "<configured provider ID>"
}
```

The server re-reads authoritative discovery inputs, proves that the selected
path is currently available, pins the exact authentication method and provider
ID, and creates a five-minute `web_login` transaction. The transaction stores
only hashed bearer proofs and safe, bounded metadata. PostgreSQL supplies the
creation time and expiry.

The successful `201` response contains only the opaque transaction handle and
its Unix-millisecond deadline:

```json
{
  "handle": "<opaque transaction handle>",
  "expires_at": 1770000000000
}
```

A separate high-entropy browser proof is attached as the host-only
`PROCTOR_BROWSER_LOGIN_PROOF` cookie with `Path=/api/v1/auth/browser/logins`,
`HttpOnly`, `SameSite=Lax`, and `Secure` outside the explicit loopback HTTP
development mode. The response is `no-store`. The handle and cookie are both
required for every browser-facing transition; neither alone proves possession.
A new action uses a new transaction rather than changing the path on an
existing transaction.

The initial implementation may support one active ordinary-login transaction
per browser cookie context. If it does, creating another must explicitly
supersede the previous pending transaction and destroy its proof; it must not
let two transactions silently share one cookie. Supporting parallel tabs later
requires transaction-specific cookie slots and tests rather than a change to
the purpose or terminal result.

## Local-password leg

`POST /api/v1/auth/browser/logins/{handle}/password` requires the matching
browser proof and accepts only:

```json
{
  "login_id": "<email or username>",
  "password": "<current password>",
  "mfa_code": "<optional current TOTP or unused recovery code>"
}
```

`mfa_code` is omitted until requested. The route does not accept `client_type`,
device presentation, destination, provider, Session options, or browser proof
in JSON. Password and MFA failures use the existing bounded authentication
Problem codes. Invalid credentials and an incomplete MFA challenge leave the
same transaction pending while its deadline and attempt limits remain valid;
they never retain the submitted credential.

The success operation atomically rechecks the transaction, path, Access
Policy, User and credential state, MFA requirement, Session limits, and
authoritative time; consumes any recovery code; creates the Web Session and
its audit; and moves the transaction to `outcome_ready`. No Session may commit
without the matching terminal outcome, and no outcome may claim success
without that Session commit.

The route attaches the ordinary host-only Session and CSRF cookies and returns
only the fixed completion location described below:

```json
{
  "completion_url": "/authorization/complete#result=<opaque selector>"
}
```

It never returns Session credentials in JSON. Exact replay after the atomic
commit returns the same safe completion location and creates no second Session.

## External-provider leg

After creating a provider transaction, the page performs a top-level,
same-origin navigation to
`GET /api/v1/auth/browser/logins/{handle}/provider`. That route requires the
matching browser proof, rechecks the pinned provider, creates one linked
`ExternalLoginState`, attaches the existing provider-protocol browser binding,
and redirects to the provider. The browser never constructs or receives a
provider authorization URL from JavaScript.

The linked external state contains the browser-transaction ID, the closed
ordinary-login purpose, exact provider, hashed state and binding proofs,
authoritative expiry, and no return URL. It contains no password, provider
token, assertion, claims, User-selected destination, or terminal prose.

The callback resolves and consumes the external state, validates the exact
provider, and completes only its linked browser transaction. Success resolves
or provisions the User under current provider admission rules and atomically
creates the Web Session, safe audit, and `outcome_ready` transaction state.
The callback attaches ordinary Session cookies only after that commit.

A failure occurring after a valid browser transaction has been resolved is a
terminal browser outcome, not a raw Problem Details page. The server consumes
or closes the external state, moves the linked transaction to
`outcome_ready` with one safe failure reason, and redirects to the fixed
completion location. This includes provider denial, invalid or expired
provider response, an unavailable provider adapter, an unlinked or conflicting
account, failed admission, Session-limit failure, and an internal terminal
failure after the callback has been safely bound.

Callback recovery first uses the returned state proof. If state is absent or
cannot be parsed, the server may use the high-entropy provider-protocol browser
binding only to locate and close the one matching pending external state. The
binding never substitutes for provider authentication and never permits
success. An unsolicited callback that cannot be bound to a valid transaction
may still return generic Problem Details because there is no trustworthy
browser journey to resume.

Provider initiation failures after a valid browser transaction has been
created use the same terminal failure path. Failures before any transaction or
proof can be resolved remain generic protocol errors. No failure reflects a
provider error, description, email, subject, or arbitrary query value into the
completion URL or rendered page.

## Terminal result

Every browser-facing transition into `outcome_ready` produces one opaque
completion selector and the fixed location:

```text
/authorization/complete#result=<opaque selector>
```

The selector is exactly one canonical unpadded base64url encoding of 256 random
bits. It is deliberately not a bearer credential: it selects one result only
when accompanied by the transaction's HttpOnly browser proof. It contains no
encoded outcome or identity. The terminal transaction retains the selector in
a form that lets an exact callback or password replay return the same location
without minting another Session. The selector is excluded from ordinary logs,
audit fields, analytics, screenshots, and error prose even though the fragment
is not sent in the navigation request.

The page bootstrap removes the complete fragment from browser history before
interpreting it, keeps a valid selector only in memory, and posts it to
`POST /api/v1/auth/browser/logins/result`. The request has the strict body:

```json
{ "result": "<opaque selector>" }
```

The Store atomically matches selector and browser proof, checks expiry, returns
one safe outcome, marks the transaction completed, and destroys the handle,
proof, selector, and remaining external state. The HTTP response clears the
browser-login proof cookie and returns one member of this closed union:

```json
{ "outcome": "authenticated" }
```

```json
{
  "outcome": "failed",
  "reason": "provider_rejected"
}
```

An `authenticated` result is presentation-only. The browser must still prove
the Web Session with `GET /api/v1/users/me`; a successful result can never be
used as a substitute credential. Replayed, expired, malformed, mismatched, or
already consumed results return one generic invalid-result Problem without
revealing which proof failed or whether a Session was ever created.

The initial safe failure reasons are:

| Reason | Meaning presented by the hosted page |
| --- | --- |
| `provider_rejected` | The provider sign-in was cancelled or rejected |
| `account_unavailable` | The proved provider account cannot be used for this installation |
| `provider_unavailable` | The configured provider could not complete the request |
| `session_unavailable` | Authentication succeeded but a Web Session could not be created under current limits |
| `try_later` | A bounded attempt or service limit requires a later attempt |
| `service_unavailable` | The sign-in could not be completed safely |

The application maps internal and provider-specific failures into this union.
It never transports raw provider codes or prose. Unknown internal failures map
to `service_unavailable`; the webapp also treats an unknown future reason as
that generic case.

## Reload, interruption, and recovery

`/authorization/complete` without an in-memory selector is a normal state. It
occurs on reload, Back, a copied path, or after fragment sanitization. The page
checks `GET /api/v1/users/me`: `200` proves an existing Web Session, `401`
offers a fresh `/login`, and network or `5xx` failure offers retry. It does not
guess a prior transaction from cookies.

Consuming a failed outcome never retries the provider callback or reopens its
state. Returning to `/login` and activating a method creates a fresh
transaction. A failed transport attempt may retry the same selector while the
transaction remains `outcome_ready`. If the result was consumed before its
response was lost, replay is the generic invalid-result case and the page
recovers by checking the actual Web Session. A lost provider callback response
may replay only to the same retained completion location and can never repeat
identity, Session, or audit effects.

## Store and lifecycle contract

`web_login` adds a purpose-specific `outcome_ready` phase before `completed`.
Pending authentication may become outcome-ready success, outcome-ready
failure, expired, or explicitly superseded. Only terminal result consumption
moves outcome-ready to completed. The generic browser transaction model must
validate fields per purpose rather than weakening desktop or Invitation
invariants to accommodate login.

The Browser Authentication Store exposes named operations for:

- creating a password or provider web-login transaction;
- resolving a pending transaction for its pinned path;
- linking one external-provider state;
- completing password login atomically with Session and audit;
- completing provider login atomically with identity resolution or admission,
  Session, audit, and external-state consumption;
- closing a bound provider attempt with one safe failed outcome and audit;
- consuming one terminal result; and
- expiring and purging bounded pages.

Application code never loads a mutable aggregate and chooses the next state.
PostgreSQL obtains one authoritative timestamp, locks every affected row,
enforces exact replay, and destroys bearer material in each named transition.
External state and browser transaction changes that must agree commit in one
database transaction.

Pending and outcome-ready records expire at their PostgreSQL deadline and
destroy all remaining proofs. Completed, superseded, and expired records retain
only safe transaction metadata for 24 hours, then bounded maintenance purges
them. Neither result selectors nor provider state extend Session lifetime or
retention.

## Transport, privacy, and verification

All browser-login and terminal-result responses are `no-store` and use the
hosted-page CSP, referrer, frame, MIME, origin, cookie, and CSRF contract. The
protocol admits only Web clients. It never accepts Desktop device metadata or
creates a Desktop Session.

Logs, metrics, audit, Problems, and traces contain only safe internal IDs,
closed reasons, provider IDs where already allowed, timing, and outcome. They
exclude transaction handles, browser proofs, completion selectors, external
state and binding values, credentials, provider assertions and tokens, email,
subject, claims, cookies, and rendered provider prose.

The vertical implementation is not complete until tests prove:

- password, MFA, provider, and every safe terminal outcome;
- current-policy and User-state rechecks at terminal commit;
- exact replay, lost-response recovery, expiry, supersession, and maintenance;
- callback binding with valid state and failure-only recovery by browser
  binding;
- no Session on failed, mismatched, expired, or replayed transactions;
- at most one Session and one terminal audit under concurrent completion;
- fragment removal, result consumption, cookie clearing, and Session
  confirmation;
- cross-node continuation using PostgreSQL rather than process memory or sticky
  routing; and
- agreement among runtime routes, OpenAPI, generated webapp types, Store
  conformance suites, and public Problem codes.
