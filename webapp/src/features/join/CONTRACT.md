# Invitation acceptance page contract

`/join` captures an `invitation_claim` from the URL fragment before render and
exchanges it once through `POST /api/v1/auth/browser/invitations`. The claim is
never rendered, copied into a query string, provider state, storage, logs, or
feedback. React Strict Mode reuses one in-flight exchange promise.

The returned purpose and requirement decide the closed terminal operation. An
`account` transaction collects username, password, and optional first and last
names, then calls the account-creating acceptance endpoint. A `session`
transaction collects no profile or credential fields and calls only the
current-Session acceptance endpoint. Neither path invents Invitation target,
recipient, Role, or membership details that the browser projection omits.

Acceptance is one atomic Invitation-package transition. Success clears local
credentials and presents a sign-in return path. Invalid, expired, consumed,
superseded, or otherwise unusable claims share bounded recipient-safe copy.
Retryable infrastructure failure preserves the purpose-bound browser handle.
The optional Institution name comes only from public discovery and never gates
acceptance.
It is read only from the shared, fully validated, same-origin discovery result;
malformed or origin-mismatched discovery is treated as absent presentation.

The ready account and Session variants use one centered task column. The
Institution purpose precedes the current acceptance task, and stable package
evidence appears through the shared `Notice` beside the controls it qualifies;
the page does not present decorative or server-backed workflow steps.

Invitation action modules validate the declared acceptance response and
classify account and Session failures into separate bounded semantic outcomes.
Presentation never receives a Problem Details code, response identifier, or
arbitrary server value.
