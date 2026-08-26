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
