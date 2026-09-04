# Proctor licensing

Proctor is a multi-module repository with licenses applied by directory.

- The repository-level [`LICENSE`](LICENSE) contains the default GNU Affero
  General Public License, version 3. More specific module, directory, and
  file-level declarations below override that default for their scope.
- The combined server in [`server/`](server/) and its browser application in
  [`webapp/`](webapp/) are licensed under the GNU Affero
  General Public License, version 3. See [`server/LICENSE`](server/LICENSE).
  Individual Mattermost-adapted source files may retain their compatible Apache
  License 2.0 notices and SPDX identifiers, as recorded in
  [`server/NOTICE`](server/NOTICE).
- Reusable modules in [`packages/`](packages/) are licensed under the Apache
  License, version 2. Each module contains its own `LICENSE` and `NOTICE`.
- Repository-owned development tooling under [`build/`](build/) and editor
  examples under [`contrib/`](contrib/) are licensed under the GNU Affero
  General Public License, version 3, unless a file declares another license.
- Repository-owned documentation under [`docs/`](docs/), automation and editor
  configuration under [`.github/`](.github/) and [`.vscode/`](.vscode/), and
  root build or quality configuration such as `.editorconfig`,
  `.golangci.yml`, `Makefile`, and the Go workspace files are licensed under
  the GNU Affero General Public License, version 3, unless a file declares
  another license.
- [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) is adapted from Contributor
  Covenant 3.0 and is licensed separately under CC-BY-SA-4.0 as declared in
  that file.
- Third-party dependencies remain under their respective licenses.

## File headers and copied source

Every comment-capable code, executable configuration, authored template, and
tracked generated-code file carries a human-readable copyright and license
notice together with an SPDX license identifier. Formats that cannot safely
contain comments remain covered by this directory policy and the applicable
license and notice files.

Original server files use the Sudosy Labs AGPL-3.0-only header. A file copied
or substantially adapted from another project keeps the upstream copyright
first, places the Sudosy Labs modification copyright immediately below it, and
declares the license that governs that file. The server's combined AGPL license
does not replace a compatible Apache, BSD, MIT, or other notice retained on an
individual file. Unmodified copies do not claim a Sudosy Labs modification
copyright. Independently written files informed only by upstream behavior use
the original Sudosy Labs header rather than an upstream copyright notice.

External contributors retain copyright in their contributions and license
them under the license governing the destination. Contribution does not assign
copyright to Sudosy Labs. New contributor-owned files name their actual
copyright holder; modifications preserve existing notices unless the governing
license requires another notice.

Code copied or substantially adapted from another project must retain all
notices required by its governing license. Mattermost-derived server work must
also identify the adaptation in-file and be recorded in
[`server/NOTICE`](server/NOTICE) with its exact upstream repository, revision,
path, governing license, and nature of the modifications. Complete license
texts for compatible, separately licensed server source live under
[`server/LICENSES/`](server/LICENSES/).

Apache-licensed reusable packages must not contain implementation copied from
AGPL, source-available, or commercial Mattermost code.
