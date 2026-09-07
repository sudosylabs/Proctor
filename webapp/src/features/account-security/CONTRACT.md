# Account security

`/account/security` owns the hosted MFA lifecycle for the current Session:
status, explicit authenticator setup or restart, activation, Session challenge,
recovery-code regeneration, and confirmed disable. Replacing an active
authenticator uses the existing explicit disable and setup operations; the
confirmation explains the interval without MFA. It never silently disables MFA.

The bounded MFA status projection is authoritative for service availability,
recovery restriction, and authentication context. A restricted Session may
reenroll or sign out; it is not sent through ordinary account APIs. Disabling
the service does not remove a recovery restriction. Fresh primary proof and
strong assurance are checked by the server for each operation.

Setup keys, authenticator inputs, and recovery codes live only in component
memory. They never enter navigation, browser storage, logs, or third-party
requests. Setup uses manual key entry with an authenticator. Recovery codes
appear only in a successful activation or regeneration response. An explicit
download or manual saving and a required acknowledgement precede leaving that
step. The acknowledgement is a User action, not evidence that a file was saved.
Refreshing or closing the page can lose this one-time view.

An uncertain activation or regeneration response leaves the mutation flow and
offers authoritative status refresh. Active MFA with lost codes requires an
explicit Session challenge and regeneration; status never reconstructs codes.
Restarting setup explicitly supersedes the pending secret. Returning from fresh
proof restores current status and never replays a sensitive action.

Feature transport validates response shapes and maps Problem codes into bounded
outcomes. Components render localized copy and share the existing task shell,
inputs, buttons, feedback, and announcement patterns. State replacements after
User actions focus the new heading; initial reads do not take focus.
