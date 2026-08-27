# Reset-password page contract

`/account/reset-password` receives a `password_reset_token` from the sanitized
fragment bootstrap. The raw token remains in one in-memory ref, never appears
in rendered content, a query string, storage, logs, analytics, or error copy,
and is destroyed after a terminal success or unusable-link response.

The page requires an explicit form submission containing a new password and a
matching browser-only confirmation. It sends only the token and new password to
`POST /api/v1/auth/password-reset/complete`. Successful completion clears both
fields and explains that existing Sessions were revoked. Expired, consumed,
superseded, malformed, and concurrent-loser credentials share one bounded
unusable-link state.

Password-policy rejection remains adjacent to the new-password field. General
failure reserves stable `FormFeedback` space and permits retry while the token
may still be usable. The one-column task introduction explains the single-use
recovery context without a progress indicator or additional server state.
Pre-action and terminal explanatory evidence uses the shared `Notice`
treatment.

The completion action accepts only the declared HTTP 204 response and owns the
mapping from concealed token failure, password rejection, rate limiting, and
unknown or transport failure into semantic results. Presentation never receives
or compares a server problem code.
