# Focused task-state contract

`TaskState` is the terminal, loading, unavailable, and context-replacement
skeleton proven by password recovery, Invitation acceptance, provider
connection, and Desktop authorization. It owns one ordered purpose label,
display-scale `h1`, supporting body, and responsive action rhythm. Feature
modules retain state, copy, evidence, maximum width, and available actions.

During initial or replacement loading, `busy` retains the purpose label and
an accessible page heading while the shared Loading indicator displays the
supporting message below its spinner. The heading is visually hidden and does
not receive focus until a completed user-triggered replacement requires it.
TaskState owns a bounded content reservation; Loading owns no page height.
The feature's existing TaskStateAnnouncement remains the one live region.

`TaskStateAnnouncement` supplies the persistent polite live region that a
feature keeps mounted while its active task changes. It announces bounded
localized state copy, never credentials, identifiers, or arbitrary server
prose. `TaskStateActions` stacks actions below `30rem` and otherwise preserves
document order.

`TaskHeading` supports opt-in programmatic focus only after a user-triggered
replacement removes the invoking control and orientation would otherwise be
lost. Initial loading, background completion, and ordinary status updates do
not focus headings. Validation continues to focus the first invalid control.

This contract excludes the email-verification outcome marker and the Session-
confirmation state rail. It also does not replace `Notice` stable evidence or
`FormFeedback` action-adjacent failures.
