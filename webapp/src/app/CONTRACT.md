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

The same directory owns root route and document orchestration. Those concerns
must not absorb feature presentation or credentials after sanitized bootstrap.
