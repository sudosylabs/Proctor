# Authorization-complete route contract

This file is the exact preimplementation contract for the server-hosted
`/authorization/complete` feature. It owns the terminal presentation after
the existing local or external login flow creates a Web Session.
[`../../../DESIGN_SYSTEM.md`](../../../DESIGN_SYSTEM.md) owns shared
presentation and interaction rules; the authentication and Session contracts
remain authoritative for server behavior.

No general authenticated shell, result component, notice, or button is created
with this feature. Reuse begins only after another real page proves identical
semantics.

## Purpose and boundary

`/authorization/complete` confirms whether the browser has the Web Session
created by the preceding login response or provider callback. It does not
authenticate credentials, consume a result token, resolve a provider response,
create or repair a Session, accept an Invitation, authorize Desktop, connect
an identity, or choose an authenticated product destination.

The first version has no dashboard or generic authenticated root to navigate
to. Success is therefore deliberately terminal: it confirms sign-in and says
that the page may be closed. A later product destination requires its own
bounded server and page contract; this route must not guess one.

External-provider failures do not arrive here. Once the server can safely bind
one to the ordinary hosted-login flow, it returns the browser to the fixed
`/login#external_login=failed` location. This keeps the terminal route's only
question exact: does the current browser hold a valid Web Session?

## URL and page inputs

The route accepts no query or fragment input. Query parameters are ignored.
The bootstrap removes any fragment before render and supplies no
purpose-specific credential, result, provider, status, destination, or prose
to the page.

On mount, the page makes exactly one same-origin
`GET /api/v1/users/me` request through the shared credential-aware client.
The browser's host-only Session cookies are the only authentication input. The
page neither reads cookies in JavaScript nor treats navigation to this path as
proof of success.

## Document and semantic structure

The page requests the stable localized document title
`Sign-in status · Proctor` from the document controller. Session state,
Institution name, User identity, and server prose never enter the title.

The visible structure contains:

- a first-focusable skip link targeting `main-content`;
- exactly one `main` landmark with `id="main-content"`;
- exactly one state-specific page heading;
- one page-owned polite live region for asynchronous status; and
- only the actions admitted by the state table below.

There is no autofocus. Initial loading and successful background transitions
do not move focus. An explicit Retry retains focus on the activated control
until the new result is available; an error summary receives programmatic
focus only after a user-triggered retry fails.

All authored copy uses `webapp.authorization_complete.*` messages in
`server/i18n`; the generated catalog is the runtime view. The implementation
does not concatenate translated fragments and never displays protocol codes or
server-provided prose.

## State machine

The feature owns these mutually exclusive states:

| State | Entry | Available action |
| --- | --- | --- |
| Confirming Session | The initial or user-triggered `GET /api/v1/users/me` is pending | None; announce bounded progress |
| Signed in | A valid User response returns `200` | None required; explain that the page may be closed |
| No active Session | The request returns `401` | Normal same-origin link to `/login` |
| Confirmation unavailable | Network failure, malformed success data, or `5xx` | Retry Session confirmation and a normal same-origin link to `/login` |

The page performs no automatic timed retry, redirect, logout, refresh-token
call, or provider restart. It never uses animation as the only indication of
pending work.

Only a valid `200` response proves Signed in. The page may validate the
minimum generated User shape needed to reject malformed success data, but it
does not render profile fields or retain the response after selecting the
state. A `401` does not claim that login failed or that a Session was lost;
it says only that this browser has no active Session.

Retry repeats only `GET /api/v1/users/me`. It prevents duplicate requests
while pending and never replays a password submission or provider callback.
Reload, Back, a bookmark, and direct clean-path navigation therefore enter the
same deterministic Session check.

## Content and action contract

Signed in uses the heading intent “You’re signed in” and supporting copy that
the page may be closed. It does not expose User fields, add a logout action, or
link to an undeclared authenticated route.

No active Session uses the heading intent “You’re not signed in” and a normal
`Sign in` link to `/login`. Confirmation unavailable uses the neutral intent
“We couldn’t check your sign-in,” an explicit `Try again` action, and the same
normal login link.

Every link remains functional without client-side interception. Buttons are
used only for in-page retry. State is not communicated by color or icon alone;
an icon, if later admitted, is decorative beside complete text.

## Security and privacy invariants

- The page accepts no credential, provider result, failure code, identity, or
  destination from the URL.
- Unknown fragments are removed before render rather than retained in history,
  the DOM, copied text, screenshots, or referrers.
- User fields, provider values, cookies, Session IDs, and raw Problem fields are
  never rendered, logged, or persisted.
- Session confirmation is same-origin and uses the server-owned credential
  transport.
- The page does not infer authentication from client state, a navigation, an
  earlier successful response, or the presence of a cookie name.
- Returning to `/login` starts a fresh user action. No provider callback or
  credential request is replayed.
- The page does not override the server-owned no-store, Content Security
  Policy, referrer, frame, MIME, origin, or cookie contract.
- Theme, locale, reduced-motion, and accessibility preferences contain no
  authentication state.

## Implementation gate

This page can be implemented against the current generated
`GET /api/v1/users/me` contract. It requires no new server model, Store
operation, persistence state, result endpoint, cookie, or OpenAPI route.

The small external-login failure redirect belongs to the existing provider
start and callback behavior and returns to `/login`; it is not a prerequisite
for implementing this Session-confirmation page.

## Acceptance

The feature is complete only when tests cover:

- Session-confirmation `200`, `401`, malformed success, network failure,
  `5xx`, retry, and duplicate-request prevention;
- reload, Back, direct clean-path navigation, query ignoring, and fragment
  removal;
- absence of automatic provider retry, automatic redirect, logout, or invented
  authenticated destination;
- keyboard order, skip-link behavior, retry focus, live announcements, visible
  text, forced colors, and `200%` zoom;
- light, dark, and system themes; reduced motion; short and expanded localized
  copy; and compact, tablet, and desktop design-system viewports; and
- absence of provider, identity, User, Session, cookie, and raw server values
  from URLs after bootstrap, storage, logs, rendered output, screenshots, and
  test artifacts.
