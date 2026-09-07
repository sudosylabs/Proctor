# Input field contract

`InputField` is the domain-neutral single-line field proven by the hosted
login, setup, and registration forms. It owns the visible label, required
marker, native input, optional description, inline error, and their stable
programmatic associations. `PasswordField` adds the repeated password
disclosure behavior without owning password policy, validation, credentials,
or submission state.

Every required field receives both the native `required` attribute and a
visible asterisk. The asterisk is decorative because the native constraint
already exposes the state to assistive technology. `errorMessage` controls
`aria-invalid` and an associated inline error; `description` and `describedBy`
are combined without replacing one another. Consumers provide the correct
`name`, `type`, `inputMode`, `autoComplete`, capitalization, spellcheck,
controlled value, and change behavior for their real field.

`PasswordField` renders an ordinary password input until the user activates
its trailing button. That button contains only the governed eye icon, remains
centered inside the input boundary, has localized `aria-label` and `title`
text, and exposes its toggle state with `aria-pressed`. The icon is decorative.
Password paste remains available, and visibility never enters the URL or any
transport payload.

Both fields accept `className` for their outer layout and `inputClassName` for
a justified local input seam. They own control height, padding, border,
typography, theme, hover, disabled, error, focus, and forced-colors behavior.
They do not own multiline text, selection controls, date/time controls, or
feature-specific state.

`OneTimeCodeField` is the six-digit variant used by authenticator enrollment
and Session proof. It wraps the pinned `input-otp` primitive with one labelled
native text input (`inputmode="numeric"`, `autocomplete="one-time-code"`) and
six decorative slots. There is one tab stop; normal selection, arrow keys,
Backspace, leading zeroes, typing, paste, and autofill retain native input
semantics. Spaces and hyphens may separate pasted digits; letters are rejected.
Completion never submits automatically.

The active slot owns the visible focus ring; a stationary caret needs no
motion. Error, disabled, hover, both themes, forced colors, and narrow or zoomed
layouts preserve the same field semantics. Digits remain left-to-right in an
RTL document. Values, validation decisions, submission, and alternative proof
methods remain consumer-owned. Browser tests cover real editing and the
production bundle under the server's Content Security Policy.

The pinned primitive normally inserts an inline compatibility stylesheet.
Proctor bundles the attributed `InputOTPCompatibility.css` instead and supplies
the primitive's `input-otp-style` guard marker in `index.html` before mounting.
An upgrade must verify that guard and the static selection, autofill, and iOS
rules against upstream, then pass the production-CSP browser test. The React
application disables the primitive's inline no-script fallback; it does not
add a CSP nonce or allow inline styles.
