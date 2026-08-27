# Provider-connection page contract

`/account/connect-provider` is an authenticated identity-linking surface, not
an ordinary external login chooser. It loads the current User and the dynamic
public provider projection, then starts exactly one
`POST /api/v1/authentication-methods/providers/{provider_id}/connect`
transaction after an explicit selection and action.

The start endpoint remains authoritative for strong and recent Session proof.
Its server-returned redirect is the only external navigation target. The page
never receives or renders provider subjects, claims, email, credentials,
tokens, protocol configuration, or identity-match hints. It states explicitly
that connection neither replaces the User's password nor changes the Proctor
profile, and provider email never merges Users.

No-Session, no-provider, loading, retryable failure, and reauthentication
states remain distinct. Several providers form one accessible radio group;
one provider retains the same explicit Continue action and never redirects on
page load.

The ready chooser uses one centered task column and a visible native radio
indicator. Two compact shared `Notice` instances keep the password/profile and
email non-merge guarantees together without introducing a progress rail or
provider artwork.

Loading, signed-out, unavailable, and empty-provider states use the shared
`TaskState` hierarchy and persistent polite announcement. Initial context
loading never takes focus. After an explicit retry replaces the retry control,
the resulting task heading receives focus so the new context is clear.
