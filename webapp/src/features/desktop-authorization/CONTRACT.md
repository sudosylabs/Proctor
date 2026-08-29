# Desktop authorization page contract

`/authorize/desktop` is the same-tab authentication and confirmation surface
for one purpose-bound Desktop Authorization. On first entry, bootstrap removes
the complete fragment and the `request` query parameter from browser history
before interpreting them; only the bounded opaque `state` remains in the URL.
The page exchanges the in-memory handle, fragment proof, and state exactly once
for the server's route-scoped HttpOnly browser cookie. It never renders, logs,
stores, or copies those values.

After binding, every operation relies on that cookie. A current Web Session is
offered automatically as an identity proof but is not required. If it is
missing, the page presents the transaction's current local and external
authentication methods in the same tab. Local authentication proves the
account only for this transaction and creates no Web Session. Provider
authentication navigates the same tab and returns to
`/authorize/desktop?state=...`; the existing browser cookie resumes the
transaction without the original proof.

The confirmation state shows only the safe public Institution presentation,
the authenticated User's canonical username, and the bounded Desktop device
label. Approval is always explicit and posts only the exact state; successful
approval navigates only to the server-returned loopback redirect. “Use another
account” clears only the transaction's authenticated account and returns to
method selection. Cancellation posts the exact state, clears the binding, and
terminally replaces the task. No Desktop Session credential, provider token,
password, raw proof, or account identifier enters browser history.

Missing, rejected, expired, consumed, and malformed requests share one
unusable-request state. An active-Exam Session lock has a separate bounded
message that directs the User back to the Desktop Session already taking the
Exam without exposing Attempt identifiers. Retryable failures do not move
focus or silently authenticate or approve.

The journey uses one centered task column. Checking and terminal states use
the shared `TaskState` hierarchy and persistent polite announcements.
Authentication and confirmation use `AccessTaskIntro`, document-order controls,
and persistent `FormFeedback`. Action-caused terminal transitions focus their
replacement heading; initial invalid state and background checks do not.
