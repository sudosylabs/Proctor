# Notice contract

`Notice` is the domain-neutral inline evidence pattern shared by the hosted
access and onboarding pages. It presents short informational, successful,
warning, or failure copy with one narrow semantic rule. It deliberately has no
tinted panel, enclosing border, radius, shadow, or decorative icon.

The `accent`, `information`, `success`, `warning`, and `danger` tones select
only the rule color. Visible text remains authoritative, so a tone never
supplies or replaces meaning. The owning feature provides localized content
and chooses the tone from its bounded state; arbitrary server prose never
enters the component.

`Notice` renders a neutral `div` and adds no ARIA role or live-region behavior
by default. The feature owns whether static content is a `note`, whether a
changed message is announced through its existing live region, and whether a
form-level error summary receives programmatic focus. Native `div` attributes,
refs, and `className` are forwarded for those semantics and local layout only.
The stable `data-proctor-notice` and `data-proctor-notice-tone` attributes are
diagnostic test hooks; product behavior must not branch on them.

The component owns its type, logical padding, state rule, forced-colors
boundary, and wrapping behavior in both themes. Consumers may set placement or
measure but must not replace its color, typography, rule, background, radius,
or internal spacing. It does not implement toasts, overlays, dismissal,
actions, route-wide status layouts, or field-level validation.
