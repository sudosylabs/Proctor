# Browser application orchestration contract

## Initial resource lifecycle

`AsyncResource.ts` owns the in-process lifecycle shared by hosted routes that
load one bounded initial result. It reuses the current attempt promise when
React Strict Mode remounts an effect, assigns a monotonically newer identity to
each retry, ignores stale completions, stops notifying an unsubscribed
component, and exposes loading for every attempt.

The hook surface is intentionally smaller than that machinery: a route supplies
one loader and initial value, then receives the current value, loading and
resolution facts, an explicit retry, and a replacement operation for
feature-owned terminal transitions. A loader must already convert remote
failure into its feature result; the lifecycle does not interpret HTTP,
Problem Details, feature states, copy, focus, or recovery policy.

Invitation acceptance is a fit test rather than an exception: its purpose-bound
exchange and optional Institution discovery remain distinct feature requests,
but one route-owned loader composes them with `Promise.all` before handing the
combined result to the lifecycle.

The same directory owns root route and document orchestration. The document
synchronizer applies the root state and theme-color metadata from the shared
resolved product theme; it does not independently infer an effective theme.
Those concerns must not absorb feature presentation or credentials after
sanitized bootstrap.

## Hosted route descriptors

`HostedRoutes.tsx` is the single authored, exhaustive counterpart to the
generated `routes.ts` membership catalog. Every generated hosted route must
declare exactly one localized document-title key, fragment policy, purpose-
specific bootstrap projection, and page renderer. The descriptor map is typed
over the generated route union, so a newly generated route fails compilation
until its authored behavior exists; the completeness test independently checks
the runtime key set.

Fragment credentials are captured and the complete fragment is removed from
history before React renders. The bootstrap union preserves distinct Invitation
claim, password-reset token, email-verification token, and Desktop browser-proof
types; it never exposes a general token field. Credential-free routes remove
unexpected fragments without interpreting them, login recognizes only its
exact bounded failure notice, Desktop query evidence remains length-bounded,
and unknown paths return no bootstrap or fallback page.
