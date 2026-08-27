# Desktop authorization page contract

`/authorize/desktop` is a confirmation surface for one purpose-bound Desktop
Authorization. The root bootstrap removes `proof` from the fragment before
render and combines it in memory with bounded `request` and `state` query
values. The page never renders, logs, stores, or copies any of those values.

The primary state requires a current browser Session, safe public Institution
presentation, and the authenticated User's canonical username. Approval posts
the exact three proofs and navigates only to the server-returned loopback
redirect. Cancellation posts the same proof set and terminally replaces the
confirmation task. No Desktop Session credential or provider token enters the
browser page.

Missing or rejected proofs share one unusable-request state. A signed-out
browser retains the in-memory request while offering sign-in in a separate tab
and an explicit check-again action; navigation in the authorization tab would
otherwise destroy the fragment proof. Retryable failures do not move focus or
silently approve. Device labels are not shown because the current browser
projection does not safely return them.

The confirmation uses one centered task column with a plain key-value evidence
list, one bounded warning `Notice`, and the approval actions in document order.
It is not vertically centered like a terminal status and introduces no device
or progress illustration.
