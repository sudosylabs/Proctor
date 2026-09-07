# Fresh Session proof

`/account/reauthenticate` refreshes proof for the current Session without creating
a new login. Its only continuation tasks are `security` and `connect-provider`,
mapped to exact local paths. Query input cannot select an arbitrary return URL.

The bounded security context selects the original password or external method.
External reauthentication starts only after an explicit action; the server
selects the original provider and returns the protocol redirect. The callback
returns to the intended task without replaying its mutation. Password proof
uses the dedicated current-Session endpoint. A separate MFA challenge supplies
strong proof when needed; it never substitutes for fresh primary proof.

Passwords and challenge codes live only in component memory, are cleared after
submission, and never enter URLs or persistent browser storage. Recovery
restrictions send the User to reenrollment instead of ordinary account tasks.
Read failures and proof failures retain localized, action-adjacent feedback.
