# Transactional-mail template workflow

This directory is the exact maintainer contract for Proctor transactional-mail
presentation. It contains one authored MJML source, one authored plain-text
source, and one tracked generated HTML file for every closed mail key. Human
copy is not authored here; the flat server-owned catalogs under
[`../i18n`](../i18n) supply localized fields to
both alternatives.

The checked-in foundation is intentionally visually neutral. Maintainers may
replace the MJML presentation without changing delivery logic, but must retain
the semantic reading order, complete text equivalent, contextual escaping,
accessibility, and privacy constraints in the
[transactional-mail architecture](../../docs/architecture/mail.md).

## Typed properties

Every MJML and text source starts with a non-rendering comment listing the
exact `templates.Properties` fields it receives:

- `.Copy.Subject`
- `.Copy.Preheader`
- `.Copy.Heading`
- `.Copy.Body`
- `.Copy.ActionLabel`
- `.Copy.Footer`
- `.ActionURL`

The four Personal Access Token security notices additionally receive the
closed `.Copy.PersonalAccessToken` label set and these bounded fields under
`.PersonalAccessToken`: `.Description`, `.ExpiresAt`, `.ActionAt`,
`.ScopeContext`, and `.ActionCount`. They never receive the one-time
credential, stored token hash, or complete action list.

The four Exam Sitting schedule messages additionally receive the closed
`.Copy.SittingSchedule` label set and these bounded fields under
`.SittingSchedule`: `.ExamTitle`, `.ClassDisplayName`, `.StartsAt`, `.EndsAt`,
and `.Timezone`. They never receive Exam instructions, resources, roster
contents, private cancellation rationale, or the actor's identity.

The four Exam Manager relationship notices additionally receive the closed
`.Copy.ExamManager` label set and these bounded fields under `.ExamManager`:
`.Title`, `.Relationship`, and `.ActionAt`. They never receive the actor,
authorization grants, other Managers, or private audit detail.

The three Class membership notices additionally receive the closed
`.Copy.ClassTransition` label set and these bounded fields under
`.ClassTransition`: `.PreviousClassDisplayName`, `.ClassDisplayName`,
`.StartsAt`, `.EndsAt`, and `.Timezone`. Enrollment and ending omit the
previous Class; open-ended enrollment omits the end time. They never receive
the actor, roster contents, authorization grants, or private audit detail.

The two Exam Submission receipt messages additionally receive the closed
`.Copy.SubmissionReceipt` label set and these bounded fields under
`.SubmissionReceipt`: `.ExamTitle`, `.SittingID`, `.SubmissionID`, `.SealedAt`,
and `.Timezone`. They never receive answers, workspace paths or selectors,
manifest contents, integrity signals, Session or continuity credentials,
candidate profile fields, or private review state.

The released-result availability message additionally receives the closed
`.Copy.ResultRelease` label set and these bounded fields under
`.ResultRelease`: `.ExamTitle`, `.ReleasedAt`, and `.Timezone`. It never
receives a score, academic outcome, candidate remarks, manager notes,
decisions, evidence, rationale, Submission or Workspace data, or an action
link.

The renderer accepts no arbitrary map and registers no custom template
functions. Copy is markup-free and HTML is parsed with Go `html/template`, so
localized and dynamic values are contextually escaped. Action URLs are
server-constructed absolute HTTPS URLs; templates never construct routes.

## Commands

Install the exact lockfile toolchain once after checkout:

```sh
make -C server/templates install
```

Regenerate tracked HTML after changing MJML or a partial, then verify that the
result is fresh and deterministic:

```sh
make -C server/templates generate
make -C server/templates check
make -C server/templates test
```

Render the complete English catalog, without mail delivery or production
data, into a caller-selected directory:

```sh
make -C server mail-preview OUTPUT=/tmp/proctor-mail-preview
```

Open `index.html` in that directory to inspect every HTML alternative; each
entry also links to its plain-text equivalent. Preview output is untracked and
must not be written below this source directory.

## Adding or translating a message

1. Add the key to the closed Go mail-template catalog and add the required
   lexically sorted flat `mail.<key>.<field>` entries to `../i18n/en.json`.
2. Add matching `<key>.mjml` and `<key>.txt` sources with the exact property
   comment.
3. Regenerate `<key>.html` and run the template and Go tests.
4. Add or extend another top-level locale file using only English IDs and
   matching interpolation placeholders. A locale may be partial; every missing
   message falls back to the installation locale, then English.

The MJML compiler is a build-time dependency only. Runtime binaries embed the
generated HTML and authored text. Renderer construction parses every
production template; the later delivery composition must construct the
renderer before server readiness. Template changes require regeneration,
rebuild, and restart.
