# Proctor licensing

Proctor is a multi-module repository with licenses applied by directory.

- The combined server in [`server/`](server/) is licensed under the GNU Affero
  General Public License, version 3. See [`server/LICENSE`](server/LICENSE).
  Individual Mattermost-adapted source files may retain their compatible Apache
  License 2.0 notices and SPDX identifiers, as recorded in
  [`server/NOTICE`](server/NOTICE).
- Reusable modules in [`packages/`](packages/) are licensed under the Apache
  License, version 2. Each module contains its own `LICENSE` and `NOTICE`.
- Third-party dependencies remain under their respective licenses.

Code copied or substantially adapted from another project must retain all
notices required by its governing license. Mattermost-derived server work must
also be recorded in [`server/NOTICE`](server/NOTICE) with its exact upstream
repository, revision, and path.

Apache-licensed reusable packages must not contain implementation copied from
AGPL, source-available, or commercial Mattermost code.
