# Forgot-password page contract

`/account/forgot-password` is the public local-account recovery request page.
It accepts one email address and calls `POST /api/v1/auth/password-reset/request`.
The accepted outcome is deliberately identical for known and unknown Users and
the page never claims that an account, password, token, or mail delivery exists.

The form validates required email syntax locally, focuses the first invalid
field, keeps input stable during submission, and reserves action-adjacent
`FormFeedback` space. Rate limiting and service failure use bounded localized
copy. Successful submission clears the email and presents the generic
check-your-email state.

The recovery-request action accepts only the declared HTTP 202 response and
maps rate limiting, unknown Problem Details, malformed responses, and transport
failure into bounded semantic results. Presentation never compares a server
problem code.

The page uses one centered task column. Its recovery purpose, heading,
supporting copy, form, stable generic evidence, and actions follow document
order without implying server-backed progress. Stable evidence uses the shared
`Notice` treatment rather than a feature-owned callout. The page captures no
credential, stores no account value outside component memory, and offers only
the fixed `/login` return path.
