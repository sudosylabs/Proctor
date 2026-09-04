# Contributing to Proctor

Thank you for helping improve Proctor. Contributions may include bug reports,
design proposals, documentation, tests, accessibility improvements, security
hardening, and code.

By participating, you agree to follow the
[Code of Conduct](CODE_OF_CONDUCT.md). Report vulnerabilities through
[SECURITY.md](SECURITY.md), never through a public issue or pull request.

## Before Starting Work

Search existing issues and pull requests before opening a new one. Use a GitHub
issue for a reproducible defect or a bounded product proposal. For a large,
cross-cutting, security-sensitive, or compatibility-affecting change, discuss
the intended outcome with maintainers before investing in an implementation.

An issue is not required for a small typo, broken link, focused test repair, or
similarly self-explanatory maintenance change.

## Development Workflow

1. Fork and clone the repository.
2. Create a short branch such as `feat/exam-export`, `fix/session-expiry`, or
   `docs/security-reporting`.
3. Follow the relevant [developer guide](docs/public/developers/index.mdx) and
   the nearest component contract or module README.
4. Implement the smallest complete change, including tests, documentation,
   generated outputs, and provenance required by that change.
5. Review and verify the complete diff using the
   [contribution checklist](docs/public/developers/contribution-checklist.mdx).
6. Open a pull request that explains the outcome, important design choices,
   verification performed, and unresolved limitations.

Start with the [local development setup](docs/public/developers/local-setup.mdx).
Run focused checks while working, then the owning module gate and the highest
relevant product gate before requesting review.

At minimum:

```sh
git diff --check
make quality
```

Use `make help` and the [testing guide](docs/public/developers/testing.mdx) to
select the relevant server, webapp, reusable-module, documentation, integration,
and product checks.

## Developer Certificate of Origin

Every commit must be signed off under the
[Developer Certificate of Origin 1.1](https://developercertificate.org/). Add
the certification using Git's `--signoff` option:

```sh
git commit --signoff
```

The resulting `Signed-off-by` trailer certifies that you have the right to
submit the contribution under the applicable license. It is not a copyright
assignment. Use your own identity; do not add another person's sign-off without
their authorization.

## Licensing, Copyright, and Upstream Work

Contributions are accepted under the license already governing their
destination. The server, webapp, reusable modules, repository tooling, and
individual adapted files do not all use the same license. Read
[LICENSING.md](LICENSING.md) before contributing.

Contributors retain copyright in their work. Preserve existing copyright and
license notices. A new file must name its actual copyright holder and applicable
license; do not claim Sudosy Labs copyright unless you are authorized to
contribute on its behalf.

Before copying or substantially adapting upstream material:

1. identify the exact repository, revision, path, and governing license;
2. confirm that the license is compatible with the destination;
3. retain all notices required by the upstream license;
4. identify the adaptation in the affected source; and
5. update the applicable tracked notice or provenance record in the same pull
   request.

Do not copy Mattermost source-available or commercial code without explicit
permission. Apache-licensed reusable modules cannot contain implementation
copied from AGPL code.

## Pull Request Review

A pull request is ready for review when it has one coherent purpose, no
unrelated changes, current generated files, complete licensing information,
and honest verification results. CI must pass before merge unless a maintainer
documents a specific pre-existing or environment-dependent failure.

Maintainers may request design, test, security, accessibility, documentation,
or provenance changes. They may also close work that conflicts with the
project's scope, architecture, safety obligations, or licensing boundaries.
There is no guaranteed review or merge timeline.

Use GitHub issues for ordinary defects and proposals. Use
[support@sudosy.fr](mailto:support@sudosy.fr) only when a private security or
Code of Conduct report is required.
