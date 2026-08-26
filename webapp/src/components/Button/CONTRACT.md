# Button contract

`Button` is the domain-neutral action control proven by the hosted login,
setup, registration, email-verification, and Session-confirmation pages.
`ButtonLink` applies the same deliberate visual hierarchy to navigation while
preserving native anchor behavior. Neither component decides an action name,
destination, permission, confirmation rule, or API operation.

`primary`, `secondary`, and `text` are hierarchy variants rather than business
states. A page still exposes one visually dominant action. `Button` defaults
to `type="button"`; a submit owner must request `type="submit"` explicitly.
`isLoading` disables repeat activation, exposes `aria-busy`, and may replace
the stable action label with a localized `loadingLabel`. The owning feature
retains its live-region announcement and completion behavior.

Both components accept ordinary native attributes and `className` for local
layout only. They own target size, typography, border, theme, hover, active,
disabled, focus, reduced-motion token behavior, and forced-colors boundaries.
Navigation is never implemented with an `onClick` button, and an action is
never implemented with an anchor.
