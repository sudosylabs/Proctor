# Public registration page contract

`/register` creates one unverified local Proctor User only when the current
Access Policy permits public registration. It starts mailbox verification but
grants no Affiliation, membership, Role Binding, or Exam access.

## Discovery and admission

Before accepting input, the page requests `GET /api/v1/discovery`, validates
the versioned public projection, and pins its `canonical_origin` to the origin
serving the page.

- Loading names the bounded policy check and exposes no form.
- An uninitialized installation links to `/setup`.
- An initialized, same-origin installation with
  `capabilities.public_registration` true exposes the form and safe
  Institution presentation.
- When public registration is false, the route explains that an Invitation is
  required and does not accept an email address or imply that an Invitation
  exists.
- Origin mismatch fails closed with a reload action.
- A network failure, malformed success body, or unavailable policy produces a
  recoverable state with an explicit retry action.

The route interprets no URL credential and removes unexpected fragments before
render. Discovery values are used only for admitted capability and safe
Institution presentation; provider data and derived authentication-mode labels
do not drive registration.

## Form and request

The form contains required `email`, `username`, and `password` controls with
visible labels, stable names, suitable types and autocomplete values, and
associated validation. Required controls use native required semantics and the
shared visible required mark. The password has a centered icon-only disclosure
button with localized accessible-name and title text. The form also contains a
required, client-only acknowledgment that registration does not grant
institutional access; its checkbox and complete label form one aligned hit
target. The acknowledgment is never persisted or transported and has no
authorization meaning.

Empty or malformed required values are rejected before transport and focus
moves to the first invalid control. Username and email are trimmed at
submission; the password remains byte-for-byte as entered. Paste is never
blocked. One pending submission prevents duplicates, preserves the visible
form, marks it busy, and announces progress.

The page sends exactly this same-origin request to
`POST /api/v1/auth/register`:

```json
{
  "username": "<current username>",
  "email": "<current email address>",
  "password": "<current password>"
}
```

## Accepted and failure states

HTTP 202 clears live form values and shows a generic accepted state explaining
that a verification message follows. The state does not echo the email,
confirm delivery, create a Session, or imply any institutional relationship.
It offers a normal link to `/login`.

Problem behavior is selected only from the bounded `problem.code`:

| Problem code | Page behavior |
| --- | --- |
| `authentication.password.invalid` | Associate policy-compliant guidance with the password field |
| `authentication.registration.invalid` | Show a generic registration-details failure without identifying an existing User |
| `authentication.registration.invitation_required` | Clear the password and replace the form with the Invitation-required state |
| `authentication.registration.unavailable` | Clear the password and replace the form with the unavailable state |
| `authentication.rate_limited` | Show a form-level request-later failure |
| `authentication.rate_limit_unavailable`, `request.invalid`, unknown, malformed, or transport failure | Show a generic recoverable form failure |

Server prose and arbitrary fields are never rendered. Passwords and response
data never enter logs, analytics, storage, history, or document metadata.

## Presentation

The page uses the form-width split of `AccessPageShell`. Safe Institution
context and a three-part proof rail remain distinct from the focused form. The
rail names mailbox verification, separate institutional access, and
installation-local credentials; each statement remains available as text
without depending on color or iconography. The accepted state changes the
shell proof tone to success only after the server accepts the request.

At narrow widths the context precedes the form in one-dimensional document
flow. Both registered color themes follow the system preference; this page
introduces no persisted theme setting or third-party asset.
