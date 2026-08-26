# Login route contract

This file is the exact preimplementation contract for the server-hosted
`/login` feature. It owns page state, same-origin API orchestration, navigation,
and recovery. [`../../../DESIGN_SYSTEM.md`](../../../DESIGN_SYSTEM.md) owns the
shared presentation and interaction rules; the Access Policy, authentication,
and Session contracts remain authoritative for server behavior.

No shared authentication shell, field, button, or provider component is
introduced with this feature. The first implementation starts here and promotes
a primitive only after another real page proves the same semantics.

## Purpose and boundary

`/login` lets a person create an ordinary Web Session using either the local
credential path or one enabled external provider. It does not register a User,
accept an Invitation, authorize Proctor Desktop, connect a provider to an
existing User, manage Sessions, or choose an arbitrary post-authentication
destination.

Every explicit authentication action first creates the `web_login` browser
transaction defined by the identity-and-access
[`browser login` reference](../../../../.agents/skills/identity-and-access/references/browser-login.md).
That server-owned transaction pins the selected path, proof, lifetime, and
terminal effect. This page never recreates that state in its component model.

The first version accepts no credential or navigation input from the URL. It
does not read a fragment, and it ignores query parameters. Both local and
external success use the fixed, same-origin terminal route
`/authorization/complete`. A future caller-specific destination must be carried
by the purpose-bound browser authentication transaction; it must not be added
as a free-form `/login?return_to=` convention.

The page uses only same-origin server APIs. It has no remote base URL, issuer
selector, third-party script, font, image, analytics, or telemetry dependency.

## Page inputs

The page begins with one `GET /api/v1/discovery` request. That response is the
only initial source for:

- the canonical Proctor origin;
- whether the installation is initialized;
- the safe Institution presentation;
- whether local login and public registration are available; and
- the ordered external-provider choices.

The page does not also request `GET /api/v1/auth/providers` during initial
render. That endpoint remains useful to other clients and future explicit
refresh behavior, but two initial sources would create an avoidable race.

Before enabling an authentication action, the page parses
`canonical_origin` and requires its origin to equal `window.location.origin`.
It neither sends credentials nor follows an automatic redirect when the
origins disagree. Institution and provider display names are escaped text
content; neither may enter the document title, logs, error reports, or raw
markup.

## Document and semantic structure

The page requests the stable localized document title `Sign in · Proctor` from
the document controller. The visible structure contains:

- a first-focusable skip link targeting `main-content`;
- exactly one `main` landmark with `id="main-content"`;
- exactly one page heading whose purpose is “Sign in”;
- safe Proctor and Institution presentation;
- a local-login form when `capabilities.local_login` is true;
- an external-provider action for every descriptor in `providers`; and
- only the registration and recovery links admitted below.

There is no autofocus on page load. Loading, failure, and method availability
do not move focus. State changes are announced through one page-owned live
region; focus moves only for invalid form input or the MFA transition described
below.

All authored page copy uses `webapp.login.*` messages in `server/i18n`; the
generated webapp catalog is the runtime view. The implementation does not
concatenate translated fragments. The Proctor name, identifiers, and protocol
values remain untranslated where required.

## Discovery states

The feature owns these mutually exclusive initial states:

| State | Entry | Available action |
| --- | --- | --- |
| Loading | Discovery is pending | None; announce the bounded loading state |
| Ready | Discovery is valid, initialized, origin-pinned, and exposes at least one login method | The admitted local and provider actions |
| Setup required | `initialized` is false | A normal same-origin link to `/setup` |
| Sign-in unavailable | Initialized, but local login is false and `providers` is empty | Retry discovery |
| Origin mismatch | `canonical_origin` does not match the serving origin | None beyond a safe reload |
| Discovery failure | Network failure, malformed success data, or `access_policy.unavailable` | Retry discovery without reloading the document |

The ready state follows the full method matrix:

- local only: show the local form;
- provider only: show every provider action;
- hybrid: show the local form and every provider action with a semantic text
  separator;
- one external-only provider: still require an explicit Continue action; and
- several providers: present a labelled provider chooser without selecting or
  redirecting automatically.

The password-recovery link to `/account/forgot-password` appears only while
local login is available. The registration link to `/register` appears only
while `capabilities.public_registration` is true. `/join` is not a generic
login-page action; Invitation links carry their own purpose into that route.

Discovery retry returns through Loading and re-evaluates every capability and
provider descriptor. The page never retains a provider choice across a policy
change.

## Local login

The local form contains visible, programmatically associated controls for:

- `login_id`, labelled “Email or username”, with `autocomplete="username"`;
  and
- `password`, labelled “Password”, with `type="password"` and
  `autocomplete="current-password"`.

Both controls have stable names. The page never blocks paste, changes the
person's input while typing, or places either value in a URL, storage,
diagnostic, analytics, or error field. The server remains responsible for
normalizing and resolving the login identifier.

The first submit creates a password transaction with exactly this same-origin
request:

```json
{ "authentication_path": "password" }
```

It sends the current credential only after creation succeeds, to
`POST /api/v1/auth/browser/logins/{handle}/password`:

```json
{
  "login_id": "<current login identifier>",
  "password": "<current password>"
}
```

The page sends no `client_type`, browser proof, device presentation,
destination, or Session option. The server owns the HttpOnly transaction proof.
While transaction creation or credential submission is pending, the page
prevents duplicate submission, marks the form busy, preserves the visible
controls, and announces progress. It does not optimistically navigate or
automatically retry.

Credential or MFA rejection leaves the same transaction pending while it is
valid. A transaction-invalid, expired, or superseded response clears live
password and MFA state; the next explicit submit creates a fresh transaction.
Changing the form from its initial credential step to another authentication
method also abandons the pending password transaction rather than repurposing
it.

A successful password response is accepted only from the transaction-specific
password route and contains the fixed completion location. The ordinary
browser-cookie transport is authoritative; the page does not persist or expose
the response body or any unexpected token fields. It clears live credential
and handle state and replaces the current history entry with the returned
same-origin `/authorization/complete#result=...` location so Back does not
restore a submitted credential form.

## MFA continuation

`authentication.mfa.required` changes the same feature into an MFA challenge;
it is not presented as a failed password. The challenge adds one visibly
labelled `mfa_code` control for a current TOTP or unused recovery code with
`autocomplete="one-time-code"`. It imposes no numeric-only pattern because a
recovery code is also valid, and it never blocks paste.

The page focuses the MFA control once when the challenge appears. It retains
the transaction handle, login identifier, and password only in live document
memory long enough to resubmit the same transaction-specific request with
`mfa_code`. Those values never enter history or durable browser storage.
Successful navigation and unmount clear them. Returning to the credential step
abandons the transaction, clears the password and MFA code, and requires the
password again.

An invalid MFA code is associated with the MFA control and focuses it without
clearing the login identifier. An unavailable MFA verifier is a challenge-level
recoverable error and does not silently fall back to single-factor login.

## External providers

Each provider action uses its exact discovery `id` and visible
`display_name`. Activation first creates a provider transaction with exactly:

```json
{
  "authentication_path": "provider",
  "provider_id": "<selected discovery provider ID>"
}
```

Only after creation succeeds does the page construct the transaction-specific
same-origin URL and pass it through the existing same-origin provider
navigation boundary:

```text
/api/v1/auth/browser/logins/{handle}/provider
```

The page supplies no client type, provider return URL, failure URL, device
presentation, or provider protocol parameter and performs no client-side
redirect to a provider origin. The Proctor endpoint verifies the transaction
proof, creates the linked provider state, sets its browser binding, and owns
the provider redirect. The page clears its in-memory handle immediately before
navigation; the server-held transaction remains authoritative across the
external round trip.

Provider actions remain real text actions rather than provider-logo-only
controls. Unknown provider types receive the same neutral treatment as known
types; type never selects an unreviewed third-party asset or changes security
behavior.

## Problem handling

Behavior is selected by the bounded `problem.code`, never by matching localized
`title` or `detail`. Unknown, malformed, or non-Problem responses use the
generic recoverable failure. Server prose and arbitrary response fields are not
rendered directly. UI messages come from the webapp localization catalog.

| Problem code | Page behavior |
| --- | --- |
| `authentication.invalid_credentials` | One generic form error; do not identify the login ID, password, User state, or local-login policy as the cause |
| `authentication.mfa.required` | Enter the MFA continuation |
| `authentication.mfa.invalid_code` | MFA-control error; keep the challenge active |
| `authentication.mfa.unavailable` | Recoverable MFA challenge error; never bypass MFA |
| `authentication.sessions.maximum_reached` | Blocking form error explaining that another active Session must be resolved; do not retry automatically |
| `authentication.rate_limited` | Blocking form error asking the person to try later; no countdown is invented without a server retry interval |
| `authentication.rate_limit_unavailable`, `authentication.internal` | Generic recoverable service error |
| `request.invalid`, `authentication.password.invalid` | Generic safe form failure and a client-contract test failure |
| Any other code or transport failure | Generic recoverable failure |

Empty required controls are caught before transport, with errors placed beside
and associated with the controls. After a rejected submission, focus moves to
the first invalid control; otherwise it moves to the form-level error summary.
Changing input clears only the error made obsolete by that change. Failures do
not clear typed credentials unless the person leaves the form or explicitly
returns from the MFA step.

## Security and privacy invariants

- Every request remains same-origin and uses the shared credential-aware API
  client where that client supports the operation.
- The page never logs, reports, serializes, persists, or places in a URL a
  password, MFA code, cookie, Session identifier, or response token.
- No error distinguishes an unknown login identifier, wrong password, disabled
  User, absent password credential, or policy-disabled local login.
- No provider is inferred from a typed email domain or selected automatically.
- Authentication success comes only from the server response and host-only
  cookies; client state never manufactures a signed-in result. The terminal
  page confirms the resulting Session independently.
- Transaction handles and completion selectors remain memory-only, are removed
  from history where applicable, and are excluded from logs, storage, rendered
  errors, screenshots, and reports even though neither is sufficient without
  its HttpOnly browser proof.
- The page does not override the server-owned no-store, Content Security
  Policy, referrer, frame, MIME, or cookie contract.
- Theme, locale, and accessibility preferences contain no authentication state.

## Implementation gate

The page is intentionally blocked on the accepted browser-login protocol. The
current `POST /api/v1/auth/login` and direct provider-login routes do not
provide its purpose-bound terminal recovery and must not be wrapped behind the
new UI as if they did. Implement the server routes, Store transitions, OpenAPI
sources, generated client, and
[`/authorization/complete` contract](../authorization-complete/CONTRACT.md) as
one vertical slice before implementing this page.

## Acceptance

The feature is complete only when tests cover:

- every discovery state and the full local/provider method matrix;
- canonical-origin mismatch and malformed discovery;
- password transaction creation, local success, generic credentials failure,
  invalid/expired/superseded transaction recovery, duplicate-submit
  prevention, rate limiting, Session maximum, transport failure, and retry;
- TOTP and recovery-code MFA, invalid code, verifier unavailability, Back, and
  secret cleanup;
- one and several external providers, explicit activation, pinned provider
  transaction creation, encoded handles, the fixed provider path, and
  same-origin navigation rejection;
- keyboard order, skip-link behavior, focus after validation and MFA
  transitions, live announcements, visible labels, paste, and password-manager
  semantics;
- light, dark, and system themes; forced colors; reduced motion; `200%` zoom;
  short and expanded localized copy; and the compact, tablet, and desktop
  viewports from the design-system acceptance matrix; and
- absence of credentials and sensitive response values from URLs, storage,
  logs, rendered errors, screenshots, and test artifacts.

The test-only document-foundation fixture is removed only after this route
provides equal or stronger coverage for every foundation behavior it currently
proves.
