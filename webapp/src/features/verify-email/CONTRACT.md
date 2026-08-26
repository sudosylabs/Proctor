# Verify-email route contract

This file is the exact contract for the server-hosted
`/account/verify-email` feature. It presents and consumes the existing
purpose-specific email-verification User Token without adding a new account,
Session, profile, persistence, or transport contract.
[`../../../DESIGN_SYSTEM.md`](../../../DESIGN_SYSTEM.md) owns shared
presentation and interaction rules. The server identity and HTTP contracts
remain authoritative for token issuance, expiry, consumption, rate limiting,
and Problem Details.

## Purpose and boundary

The page lets the holder deliberately verify the one email address already
bound to a single-use verification token. It never selects a User or email,
changes profile data, creates a Session, authenticates a browser, grants an
Affiliation, membership, or Role Binding, or infers that registration granted
Institution access.

The token is not consumed automatically. Mail scanners, link previews, and a
browser preloading the page must not spend a one-time credential; the person
must activate the explicit `Verify email` button. Completion sends exactly one
same-origin `POST /api/v1/auth/email-verification/complete` request containing
only `{ "token": "<captured token>" }`.

## URL and credential handling

The mail link supplies the token as the sole `token` value in the URL fragment.
The nonvisual bootstrap removes the complete fragment from history before
interpreting it and passes a purpose-specific `email_verification_token` only
in memory. A missing, empty, malformed, or multiply-valued fragment yields the
same unusable-link presentation and makes no API request.

The token never enters a query string, document title, DOM text or attribute,
storage, cookie, log, error, analytics event, screenshot, or server-provided
prose. The feature releases its local token reference after verified or invalid
completion. An unavailable result retains it only in memory for explicit
retry.

## Document and semantic structure

The page requests the stable title `Verify email · Proctor`. It contains the
shared first-focusable skip link, one `main` landmark, one state-specific `h1`,
visible state label and supporting copy, and one polite live region. State is
never communicated by color or icon alone.

There is no autofocus on initial load. After a user-triggered completion or
retry resolves, focus moves to the resulting heading so the context change is
immediate to keyboard and assistive-technology users. Pending work disables
duplicate activation and remains named in text. Reload and Back return to the
clean path and therefore cannot replay a removed credential.

All authored copy uses `webapp.verify_email.*` messages in `server/i18n`; the
generated browser catalog is the runtime view. Raw Problem title, detail, type,
instance, and unknown code values are never rendered.

## State machine

| State | Entry | Available action |
| --- | --- | --- |
| Ready | One purpose-specific token was captured | Explicit `Verify email` button |
| Verifying | The explicit completion request is pending | Disabled pending button; no duplicate request |
| Verified | The API returns exactly `204` | Normal same-origin `Sign in` link |
| Link unavailable | No token exists, or the API returns `request.invalid` or `authentication.account_token.invalid` | Normal same-origin `Sign in` link |
| Verification unavailable | Network failure, malformed response, rate limit, or any other bounded API failure | Explicit `Try again` button and normal `Sign in` link |

The link-unavailable state does not distinguish malformed, expired,
superseded, already-used, or concurrent-loser tokens. The unavailable state
does not claim whether a request committed; retry may therefore resolve as
verified, unavailable, or the same concealed invalid-link state.

## Security and privacy invariants

- Navigation to the route is never proof that an email is verified.
- Only an exact `204` response proves completion to the page.
- The public completion request contains no Session credential or email input.
- No state automatically redirects, resends mail, signs in, or requests a new
  token.
- Requesting another verification message remains the authenticated API's
  responsibility after sign-in.
- Theme, locale, focus, motion, and responsive state carry no credential or
  identity data.
- The page preserves the server-owned no-store, Content Security Policy,
  referrer, frame, MIME, and origin contract.

## Acceptance

The feature is complete only when tests cover fragment removal before render,
missing and malformed fragment behavior, no automatic consumption, the exact
request body, duplicate prevention, `204`, concealed invalid-token outcomes,
network or `503` recovery, explicit retry, clean-path reload, title and DOM
secret absence, focus and live announcements, keyboard order, forced colors,
reduced motion, `200%` zoom, light and dark themes, and compact, tablet, and
desktop design-system viewports.
