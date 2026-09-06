# Transactional-mail template workflow

This data-only directory is the exact maintainer contract for Proctor
transactional-mail presentation. It contains no Go implementation: only build
tooling, one authored MJML source, one authored plain-text source, and one
tracked generated HTML file for every closed mail key. Human copy is not
authored here; the flat server-owned catalogs under
[`../i18n`](../i18n) supply localized fields to
both alternatives.

The HTML presentation is a single light envelope: board `#F4F4F6`, white
paper, ink `#161616`, violet `#5C00AA`, and the package-local lockup
`proctor-lockup-25d-v1.png` (600×118 transparent PNG, 200×39 display,
`alt="Proctor"`). It uses ordinary stacked
sections so clients render the same structure. Human prose still comes only
from `../i18n`. Maintainers may restyle the MJML without changing delivery
logic, but must retain the semantic reading order, complete text equivalent,
contextual escaping, accessibility, and privacy constraints in the
repository [`transactional-mail` skill](../../.agents/skills/transactional-mail/SKILL.md).

## Inline branding

The header uses the 2.5D purple mark and ink wordmark on the white paper.
`proctor-lockup.svg` is the package-local editable artwork. The released PNG
is embedded in the server and referenced by a Content-ID containing its
SHA-256 digest. The mail-owned image catalog validates its digest and dimensions
at startup; each production template must reference one released logo.
The root mail adapter supplies the PNG as an inline MIME part, so delivery
requires no remote image request.

Released PNG filenames, digests, and bytes are immutable. A later artwork
release adds a new version and Content-ID while retaining earlier versions:
queued deliveries and frozen fan-out bundles must resolve their original
artwork across upgrades and retries. Legacy frozen HTML using the old relative
image path is left unchanged; it does not acquire a different logo on retry.

The generator includes released PNGs in template source digests. After
changing the shared header or adding an image version, regenerate the HTML.
The preview command copies referenced PNGs alongside its output and resolves
Content-IDs to those local copies for browser viewing.

## Typed properties

Every MJML and text source starts with a non-rendering license header followed
by a non-rendering comment listing the exact private `server/app/mail` renderer
properties it receives. The generator emits the same license header in every
tracked HTML result before its generated-file marker:

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
`.Scope`, and `.ActionCount`. They never receive the one-time
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

1. Add the key to the closed `model.MailTemplateKey` catalog and add the required
   lexically sorted flat `mail.<key>.<field>` entries to `../i18n/en.json`.
2. Add matching `<key>.mjml` and `<key>.txt` sources with the exact property
   comment.
3. Regenerate `<key>.html` and run the template and Go tests.
4. Add or extend another top-level locale file using only English IDs and
   matching interpolation placeholders. A locale may be partial; every missing
   message falls back to the installation locale, then English.

The MJML compiler is a build-time dependency only. Runtime binaries embed the
generated HTML, authored text, and released PNGs. Renderer construction parses every
production template; the later delivery composition must construct the
renderer before server readiness. Template changes require regeneration,
rebuild, and restart.
