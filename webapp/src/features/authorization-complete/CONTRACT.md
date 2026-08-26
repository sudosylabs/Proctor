# Authorization-complete route contract

This file is the exact preimplementation contract for the server-hosted
`/authorization/complete` feature. It owns the terminal presentation after an
ordinary browser-login transaction. The identity-and-access
[`browser login` reference](../../../../.agents/skills/identity-and-access/references/browser-login.md)
owns the transaction, result, provider recovery, and Session protocol.
[`../../../DESIGN_SYSTEM.md`](../../../DESIGN_SYSTEM.md) owns shared
presentation and interaction rules.

No general authenticated shell, result component, notice, or button is created
with this feature. Reuse begins only after another real page proves identical
semantics.

## Purpose and boundary

`/authorization/complete` turns one bounded login result into a safe terminal
experience. It may confirm an existing Web Session, explain that the attempted
sign-in did not complete, or recover from a reload with no result. It does not
authenticate credentials, resolve a provider response, create a Session,
accept an Invitation, authorize Desktop, connect an identity, or choose an
authenticated product destination.

The first version has no dashboard or generic authenticated root to navigate
to. Success is therefore deliberately terminal: it confirms sign-in and says
that the page may be closed. A later product destination must be added to the
server-owned browser-login purpose and this contract together; the page must
not guess one.

## URL input and bootstrap

The only accepted input is one exact fragment member:

```text
/authorization/complete#result=<opaque selector>
```

The document bootstrap removes the complete fragment from browser history
before interpreting it. A fragment is accepted only when it contains exactly
one `result` parameter and no other member, and that value is the canonical
unpadded base64url encoding of exactly 256 bits. A valid value becomes the
in-memory hosted-page credential
`{ kind: "browser_login_result", value }`. Malformed and empty fragments are
removed and treated as no result.

The route ignores query parameters. It never accepts outcome, error, provider,
account, destination, return, retry, or prose from either query or fragment.
It never reconstructs a result after bootstrap, writes one back into history,
or stores one in local storage, session storage, IndexedDB, a service worker,
or application state that outlives the mounted page.

## Document and semantic structure

The page requests the stable localized document title
`Sign-in status · Proctor` from the document controller. Result state,
provider name, Institution name, User identity, and server prose never enter
the title.

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
| Reading result | A valid in-memory selector exists and result consumption is pending | None; announce bounded progress |
| Confirming Session | Result says `authenticated`, or the route loaded without a result, and `GET /api/v1/users/me` is pending | None; announce bounded progress |
| Signed in | Session confirmation returns `200` | None required; explain that the page may be closed |
| Sign-in failed | Result says `failed` with a known or unknown safe reason | Normal same-origin link to `/login` |
| No result | No valid selector exists and Session confirmation returns `401`, or a result is definitively invalid and no Session can be confirmed | Normal same-origin link to `/login` |
| Recovery unavailable | Result consumption or Session confirmation has a network or `5xx` failure | Retry the exact interrupted check; also offer `/login` only when no credential value would be abandoned |

The page performs no automatic timed retry, redirect, countdown, or provider
restart. It never uses animation to imply indeterminate progress indefinitely.

## Result consumption

With a valid in-memory selector, the page first sends exactly this same-origin
request to `POST /api/v1/auth/browser/logins/result`:

```json
{ "result": "<opaque selector>" }
```

The shared credential-aware client supplies the HttpOnly browser proof and
CSRF behavior. The page sends no User, provider, transaction purpose,
destination, or client type. It prevents duplicate consumption while one
request is pending.

On a transport or `5xx` failure, the page retains the selector only in live
memory and lets the person retry. On any definitive response it clears that
memory before rendering the next state.

An `authenticated` outcome does not prove sign-in to the page. It immediately
enters Confirming Session and calls `GET /api/v1/users/me`. Only a valid
`200` response proves Signed in. A `401` after an authenticated result
becomes No result with generic recovery copy; it does not claim that a Session
was lost or expose a server inconsistency. A network or `5xx` failure retains
only the need to retry Session confirmation, not the consumed selector.

A `failed` outcome enters Sign-in failed without calling the provider,
replaying its state, or automatically starting another transaction. The safe
reason selects localized copy as follows:

| Safe reason | Presentation intent |
| --- | --- |
| `provider_rejected` | The provider sign-in was cancelled or rejected; try again when ready |
| `account_unavailable` | This provider account cannot be used for this Proctor installation |
| `provider_unavailable` | The configured sign-in provider could not complete the request |
| `session_unavailable` | Sign-in could not create another active browser Session |
| `try_later` | Too many attempts or a temporary limit requires a later attempt |
| `service_unavailable` | Sign-in could not be completed safely |
| Unknown future value | The same generic presentation as `service_unavailable` |

The copy may recommend returning to `/login`; it never recommends account
merging, provider linking, administrator contact, Session deletion, or a wait
duration unless a later product flow can actually perform or support that
action.

An invalid, expired, mismatched, replayed, or consumed result uses one generic
invalid-result Problem. The page then checks `GET /api/v1/users/me` because a
response may have been lost after the server committed and attached a Session.
`200` becomes Signed in; `401` becomes No result. It does not distinguish
which part of the result proof failed.

## Reload and direct navigation

When bootstrap yields no selector, the page calls
`GET /api/v1/users/me` once:

- `200` becomes Signed in;
- `401` becomes No result; and
- network failure, malformed success data, or `5xx` becomes Recovery
  unavailable with a retry of that Session check.

This makes reload, Back, a copied clean path, and a bookmark deterministic
without reading authentication cookies in JavaScript or guessing a transaction
from browser state. The page does not call discovery merely to render a
terminal result; safe Proctor presentation comes from the shared document
foundation.

## Content and action contract

Signed in uses the heading intent “You’re signed in” and supporting copy that
the page may be closed. It does not expose profile fields from
`GET /api/v1/users/me`, add a logout action, or link to an undeclared
authenticated route.

Sign-in failed uses a heading intent “Sign-in wasn’t completed,” one localized
safe explanation, and a normal `Sign in again` link to `/login`. No result
uses a heading intent “There’s no sign-in result” with the same link. Recovery
unavailable uses a neutral “We couldn’t check your sign-in” intent and an
explicit `Try again` action.

Every link remains functional without client-side interception. Buttons are
used only for in-page retry. State is not communicated by color or icon alone;
an icon, if later admitted, is decorative beside complete text.

## Security and privacy invariants

- The result selector is treated as sensitive browser-flow material even
  though it cannot authenticate without the HttpOnly proof.
- The selector is removed before render and never appears in the DOM,
  accessible name, title, logs, metrics, analytics, error reports, screenshots,
  copied text, or test artifacts.
- Provider IDs, provider errors, subjects, claims, emails, User fields,
  transaction IDs, cookies, Session IDs, and raw Problem fields are never
  rendered or persisted.
- Result consumption and Session confirmation are same-origin and use the
  server-owned credential and CSRF transport.
- The page does not infer authentication from a client flag, the result
  selector, a successful navigation, or an `authenticated` result alone.
- Returning to `/login` starts a fresh transaction. No provider callback or
  failed state is replayed.
- The page does not override the server-owned no-store, Content Security
  Policy, referrer, frame, MIME, origin, or cookie contract.
- Theme, locale, reduced-motion, and accessibility preferences contain no
  authentication or result state.

## Implementation gate

This page must be implemented in the same vertical slice as the browser-login
server protocol. Before visual implementation, that slice must add the
`web_login` transaction purpose and terminal result Store operations, replace
raw provider callback failures for valid transactions with bounded completion
redirects, update OpenAPI and generated types, and extend the bootstrap
fragment credential union.

The existing direct login and provider routes do not satisfy this contract.
This page must not parse their Problem Details from a URL or turn a legacy
`return_to` query into the new protocol.

## Acceptance

The feature is complete only when tests cover:

- valid, empty, malformed, extra-member, and oversized result fragments,
  including removal before interpretation and render;
- authenticated and every failed safe outcome, plus unknown future reasons;
- result transport failure, `5xx`, invalid/replayed/expired result, retry, and
  duplicate-consume prevention;
- Session-confirmation `200`, `401`, malformed success, network failure,
  `5xx`, and retry after both result and no-result entry;
- reload, Back, direct clean-path navigation, and lost-response recovery;
- absence of automatic provider retry, automatic redirect, or invented
  authenticated destination;
- keyboard order, skip-link behavior, retry focus, live announcements, visible
  text, forced colors, and `200%` zoom;
- light, dark, and system themes; reduced motion; short and expanded localized
  copy; and compact, tablet, and desktop design-system viewports; and
- absence of result, provider, identity, Session, cookie, and raw server values
  from URLs after bootstrap, storage, logs, rendered output, screenshots, and
  test artifacts.
