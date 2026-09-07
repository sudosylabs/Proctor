# Authentication code entry

This field selects between the six-digit authenticator input and an ordinary
recovery-code input for hosted login, Desktop authorization, and Session proof.
Enrollment disables the recovery option. The parent owns the selected method
and code together, validation, transport, pending state, and value disposal.

Changing method clears the previous code and focuses the replacement input.
The forwarded ref always targets that live input, including after a switch.
The recovery field keeps letters, digits, and separators intact, accepts paste,
and remains bounded to the API's 256-character input limit. Authenticator entry
uses the InputField family's six-digit primitive and requires a complete code
before submission. Neither method automatically submits on completion.

Labels, descriptions, required markers, and inline errors stay programmatically
associated with the single active input. The switch is a named button, follows
the field in keyboard order, clears stale field errors through the parent's
change callback, and is disabled while submission is pending. Browser tests
exercise switching, focus, unchanged recovery-code transport, and both hosted
login and Session-proof consumers.
