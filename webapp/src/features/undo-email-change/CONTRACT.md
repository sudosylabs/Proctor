# Undo-email-change route contract

This file is the exact contract for the server-hosted
`/account/undo-email-change` feature. It presents and consumes the
purpose-specific email-change undo User Token that Proctor mails to the
previous address whenever an account's email address is changed.
[`../../../DESIGN_SYSTEM.md`](../../../DESIGN_SYSTEM.md) owns shared
presentation and interaction rules; the server identity and HTTP contracts
remain authoritative for issuance, the undo window, consumption, rate
limiting, and Problem Details.

## Purpose and boundary

The page lets the holder of the previous mailbox deliberately reverse one
email change. A successful undo restores the previous address (verified,
because the link proves control of it), signs every Session of the account
out, and records an audit event. The page never selects a User or address,
creates a Session, or reveals which address is restored.

The token is not consumed automatically: mail scanners and link previews must
not spend it. The person must activate the explicit `Restore previous email`
button, which sends exactly one same-origin
`POST /api/v1/auth/email-change/undo` request containing only
`{ "token": "<captured token>" }`.

## URL and credential handling

The mail link supplies the token as the sole `token` value in the URL
fragment. The nonvisual bootstrap removes the fragment from history before
interpreting it and passes an `email_change_undo_token` only in memory. A
missing, empty, malformed, or multiply-valued fragment yields the
link-unavailable presentation and makes no API request. The token never enters
a query string, title, DOM, storage, cookie, log, or analytics event, and the
page drops its reference after a restored, invalid, or conflict outcome.

## State machine

| State | Entry | Available action |
| --- | --- | --- |
| Ready | One token was captured | `Restore previous email` button and `Sign in` link |
| Restoring | The request is pending | Disabled pending button; no duplicate request |
| Restored | The API returns exactly `204` | `Sign in` link |
| Link unavailable | No token, `request.invalid`, or `authentication.account_token.invalid` | `Sign in` link |
| Address in use | `authentication.email_change_undo.conflict` | `Sign in` link; contact the institution |
| Undo unavailable | `authentication.rate_limited`, network failure, or any other failure | `Try again` button and `Sign in` link |

The link-unavailable state does not distinguish malformed, expired, already
used, or superseded tokens. Focus moves to the new heading after a
user-triggered outcome, and one polite live region announces it. All copy uses
`webapp.undo_email_change.*` messages in `server/i18n`.

## Acceptance

Tests cover the exact request body, the `204`-only success, every mapped
Problem code, safe failure on transport errors, fragment removal before render,
and the hosted route descriptor; the page reuses the verify-email status
layout, so its theme, zoom, motion, and viewport behavior follow that page.
