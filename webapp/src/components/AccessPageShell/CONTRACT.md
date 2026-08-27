# Access page shell contract

`AccessPageShell` is the shared structural frame proven by all ten declared
hosted access routes, from `/setup` and `/login` through password recovery,
Invitation acceptance, Desktop authorization, provider connection, and
terminal confirmation. It owns their Proctor identity, skip link,
single main landmark, bounded application frame, and responsive split,
single-task, or status composition.

It does not own authentication state, API orchestration, form controls,
buttons, notices, headings, Institution content, navigation decisions, or
document metadata. Feature pages provide all visible localized copy and retain
their own state semantics.

The shell renders a canonical Proctor lockup rather than reconstructing one
from the standalone mark and live text. Light presentation uses the purple
mark with ink wordmark; dark presentation uses the purple mark with white
wordmark. Neither lockup has an enclosing background or adjacent decorative
rule. Feature state is named within the task content rather than encoded in
the global brand header.

All variants preserve one-dimensional flow at narrow widths and 200% zoom.
The `split` variant introduces its dividing rule only when the form and context
can remain side by side. Its default `form` main size bounds a focused access
task; `content` admits the wider grouped form proven by `/setup` without
changing its narrow document flow. The `status` variant centers a bounded
terminal task without creating a floating card or nested page scroller.
The `single` variant gives focused access tasks one centered document column
with a consistent block start; unlike `status`, it does not vertically center
short content or imply that forms are terminal messages.

Every consumer supplies the localized skip-link label. The shell provides
`id="main-content"` and `tabindex="-1"` on its one `main` landmark so the skip
link and feature-owned validation focus behavior have stable targets.
