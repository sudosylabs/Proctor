# Proctor governance

Proctor is a pre-release project with a single maintainer. This document
describes how decisions are actually made today and how that is expected to
change. It will be revised when the project gains a second maintainer or
publishes its first supported release.

## Roles

- **Contributors** open issues, discussions, and pull requests under
  [CONTRIBUTING.md](CONTRIBUTING.md).
- **Maintainers**, listed in [MAINTAINERS.md](MAINTAINERS.md), review and
  merge changes, triage issues, handle private security and Code of Conduct
  reports, and cut releases. [`.github/CODEOWNERS`](.github/CODEOWNERS)
  requests their review automatically.
- **Sudosy Labs** owns the GitHub organization that hosts the repository.

There is currently one maintainer, so every role above that requires a
maintainer is held by the same person.

## Decisions

The maintainer decides what is merged and released. Ordinary changes are
decided in pull request review against the repository's documented
architecture, security, licensing, and contribution rules.

A change that alters the architecture guardrails in [AGENTS.md](AGENTS.md),
a public or compatibility contract, licensing, or this governance is proposed
in an issue or pull request before it is merged, so the reasoning is public
and contributors can respond. The maintainer records the outcome in that
issue or pull request, or in the owning contract or skill when it changes
durable repository knowledge.

There is no voting process while there is one maintainer. Once there are two
or more, decisions will be made by consensus among maintainers, and this
section will describe how disagreements are resolved before that happens.

## Becoming a maintainer

A contributor can be invited to become a maintainer after a sustained record
of reviewed contributions that follow the project's architecture, security,
and licensing rules. The existing maintainers make the invitation. The new
maintainer is added to [MAINTAINERS.md](MAINTAINERS.md) and to the matching
[`.github/CODEOWNERS`](.github/CODEOWNERS) entries in one reviewed pull
request; repository permissions follow that change.

A maintainer may step down at any time. A maintainer who is inactive for an
extended period, or who breaches the [Code of Conduct](CODE_OF_CONDUCT.md),
may be moved to emeritus status by the remaining maintainers.

## Releases

Each product module is versioned and released independently. The
[build and release guide](build/README.md#cutting-and-consuming-a-release)
defines the versioning policy, tag forms, signing, and verification. A
maintainer cuts a release by:

1. confirming that the target commit is on `main` and its CI and
   vulnerability gates pass;
2. moving the module's [`CHANGELOG.md`](CHANGELOG.md) entries from
   `Unreleased` under the new version in a reviewed commit; and
3. pushing one module tag for that commit and approving the protected
   `release` environment.

Publication stays disabled until the activation steps in the build guide are
complete. Proctor has not yet published a supported release.

## Security and conduct

Vulnerabilities are reported and handled privately under
[SECURITY.md](SECURITY.md), which takes precedence over the public process in
this document. Conduct in project spaces is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md); maintainers enforce it as that document
describes.
