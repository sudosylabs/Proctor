# Security Policy

Security reports help protect institutions, administrators, exam managers,
candidates, contributors, and operators. Please report suspected
vulnerabilities privately so maintainers can investigate and coordinate a fix
before public disclosure.

## Supported Versions

Proctor has not yet published a supported production release. Reports affecting
the current `main` branch or an official pre-release artifact are accepted, but
there is currently no commitment to backport fixes to older snapshots and no
response or remediation service-level agreement.

This section will be replaced with an explicit supported-version table before
the first supported release.

## Report a Vulnerability Privately

Use either of these private channels:

1. Submit a [private GitHub vulnerability report](https://github.com/sudosylabs/Proctor/security/advisories/new).
2. If GitHub private reporting is unavailable, email
   [support@sudosy.fr](mailto:support@sudosy.fr?subject=%5BProctor%20Security%5D)
   with the subject prefix `[Proctor Security]`.

Do not open a public issue, pull request, discussion, or social-media thread for
an undisclosed vulnerability.

Include what you can safely provide:

- the affected component, version, release artifact, or commit;
- the vulnerability's impact and required preconditions;
- minimal reproduction steps using synthetic data and an installation you are
  authorized to test;
- relevant configuration with credentials and private values removed;
- a suggested mitigation or fix, if known; and
- your preferred disclosure and credit details.

Never send credentials, private keys, access tokens, real student data, exam
answers, or data taken from an installation you do not own or have explicit
permission to test. If sensitive material is essential to understanding the
report, describe its nature first and wait for handling instructions.

## What Happens After a Report

Maintainers will keep the report private while they validate the issue,
determine affected versions, develop and test a correction, and coordinate
disclosure. When appropriate, disclosure will use a GitHub Security Advisory
and may request a CVE. Reporters will be credited when they request credit and
doing so is safe.

The absence of a response deadline is not permission to disclose private user
or institution data. If you plan to publish your research, state the proposed
timeline in the initial report so disclosure can be coordinated.

## Research Boundaries

This policy does not create a bug bounty or authorize access to systems, data,
accounts, or exam content that you do not own or have explicit permission to
test. Avoid privacy violations, service disruption, social engineering,
credential attacks, persistence, destructive testing, and unnecessary access
to data.

For a vulnerability in a third-party dependency, report it to the upstream
project. Also notify Proctor privately when the vulnerable behavior is
reachable through Proctor or requires a Proctor mitigation.

The public [security architecture guide](docs/public/security/index.mdx)
describes product trust boundaries and controls; it is not a vulnerability
reporting channel.
