# Access page shell contract

`AccessPageShell` is the shared structural frame proven by the `/setup`,
`/login`, `/register`, and `/authorization/complete` routes. It owns their
Proctor identity, skip link, single main landmark, bounded application frame,
responsive split or status composition, and the state-aware proof line.

It does not own authentication state, API orchestration, form controls,
buttons, notices, headings, Institution content, navigation decisions, or
document metadata. Feature pages provide all visible localized copy and retain
their own state semantics.

The shell renders a canonical Proctor lockup rather than reconstructing one
from the standalone mark and live text. Light presentation uses the purple
mark with ink wordmark; dark presentation uses the purple mark with white
wordmark. Neither lockup has an enclosing background. Its proof line is
structural: `primary` identifies an active Proctor-controlled task, `success`
represents a confirmed healthy state, and `neutral` carries pending or
unconfirmed state. A feature must still name that state in visible text.

Both variants preserve one-dimensional flow at narrow widths and 200% zoom.
The `split` variant introduces its dividing rule only when the form and context
can remain side by side. Its default `form` main size bounds a focused access
task; `content` admits the wider grouped form proven by `/setup` without
changing its narrow document flow. The `status` variant centers a bounded
terminal task without creating a floating card or nested page scroller.

Every consumer supplies the localized skip-link label. The shell provides
`id="main-content"` and `tabindex="-1"` on its one `main` landmark so the skip
link and feature-owned validation focus behavior have stable targets.
