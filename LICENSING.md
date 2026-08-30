# Proctor licensing

Proctor is a multi-module repository with licenses applied by directory.

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
- Third-party dependencies remain under their respective licenses.

Code copied or substantially adapted from another project must retain all
notices required by its governing license. Mattermost-derived server work must
also be recorded in [`server/NOTICE`](server/NOTICE) with its exact upstream
repository, revision, and path.

Apache-licensed reusable packages must not contain implementation copied from
AGPL, source-available, or commercial Mattermost code.
