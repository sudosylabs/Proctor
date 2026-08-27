# Installation setup page contract

`/setup` is the one-time operator surface for establishing this Proctor
installation. It presents the deployment-owned bootstrap proof, the single
Institution identity, and the first administrator profile as one atomic task.
It never treats the first submitted User as an implicit administrator and it
never creates a Session.

## Entry and status

The page first requests `GET /api/v1/bootstrap` and accepts only a response
with a boolean `initialized` value.

- While the request is pending, the page names the bounded check and exposes
  no form.
- When `initialized` is false, the setup form becomes available.
- When `initialized` is true, the page explains that setup is complete and
  links to `/login`; it never offers to replace the Institution or first
  administrator.
- A transport failure, malformed success body, or server failure produces a
  recoverable state with an explicit retry action.

The route interprets no URL credential. Bootstrap removes an unexpected
fragment before render, and the page never reads setup input from query
parameters, storage, or document bootstrap data.

## Atomic form

The visible form has three sections:

1. operator verification: `bootstrap_secret`;
2. Institution: required canonical `name`, required `display_name`, and an
   optional `description`; and
3. first administrator: required `email`, required `username`, optional
   `display_name`, and required `password`.

Every control has a visible label, stable name, applicable input type and
autocomplete value, associated validation guidance, and a minimum 44-pixel
target. Required controls use native required semantics and the shared visible
required mark. Password and bootstrap-secret disclosure is a centered,
icon-only button with localized accessible-name and title text. Paste is never
blocked. Optional empty profile values are omitted; required ordinary text is
trimmed at submission while passwords and the bootstrap secret remain
byte-for-byte as entered.

Empty required fields are rejected before transport. Focus moves to the first
invalid control. One pending submission disables duplicate submission,
preserves visible input, marks the form busy, and announces progress. The
page sends exactly one same-origin request to `POST /api/v1/bootstrap` with the
generated API client.

## Completion and failure

A successful response clears live secrets and presents a confirmed completion
state with a normal link to `/login`. It does not sign in, mint local
authority, infer email verification, or navigate automatically.

Problem behavior is selected only from the bounded `problem.code`:

The setup action module owns that selection and successful-response validation.
The form receives only semantic outcomes such as bootstrap denial, password
rejection, rate limiting, completion, or safe unavailability.

| Problem code | Page behavior |
| --- | --- |
| `installation.already_initialized` | Clear live secrets and show the setup-complete state |
| `installation.bootstrap_denied` | Associate a safe denial with the bootstrap-secret field |
| `authentication.password.invalid` | Associate policy-compliant guidance with the password field |
| `authentication.rate_limited` | Show a form-level request-later failure |
| `installation.unavailable`, `request.invalid`, unknown, malformed, or transport failure | Show a generic recoverable form failure |

No server title, detail, identifier, administrator profile, bootstrap secret,
password, or response resource is rendered, logged, serialized, persisted, or
placed in a URL. A failed transaction is never described as partially
complete. A general form failure remains in the reserved feedback region after
the action row and is announced politely without moving focus. The form fields
and action row remain stationary while ordinary failure copy appears.

## Presentation

The page uses `AccessPageShell` with the content-width split. Its left region
names the one-time proof chain; the main region remains a flat ordered form,
not a wizard or a large floating card. The proof rail is structural evidence
and the caution states that the Institution, administrator, protected Role
Binding, and initial Access Policy are created in one transaction. On narrow
viewports the context and form return to document order, field pairs become a
single column, and the task remains one-dimensional at 200% zoom.
