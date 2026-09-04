# Prepare pull requests

## Establish the review range

Resolve the requested base branch and remote from repository evidence. Inspect
the merge base, every commit in the proposed range, and the aggregate
`<base>...HEAD` diff. Account for committed, staged, and unstaged changes so the
pull request description never presents local-only work as published.

The range should represent one reviewable product or engineering outcome. It
may contain several coherent commits when they are ordered parts of that
outcome. Split independent outcomes into separate pull requests when they can
be reviewed, verified, and merged independently.

## Write the pull request

Use a concise title that describes the net outcome of the whole range. Follow
the commit subject form when it fits; do not derive the title mechanically from
the latest commit.

Adapt the body to the repository's pull-request template when one exists.
Otherwise include only the sections that carry information:

- context: the problem, need, or constraint;
- changes: the logical groups and their observable result;
- verification: exact checks performed and their outcomes;
- risk and operations: compatibility, migration, rollout, security, data, or
  recovery considerations that apply; and
- links or follow-up: related issues and deliberately deferred work.

Write from the aggregate diff and actual verification evidence. State missing
checks and known limitations directly. Keep incidental implementation detail in
the commits and diff unless reviewers need it to evaluate the design.

## Publish and update

Before opening the pull request, confirm the destination repository, base,
head, draft state, title, and body. Push only the branch and commits authorized
for publication. Request reviewers, apply labels, link issues, enable automatic
merge, or change draft state only when the user's request includes those shared
workflow changes.

After new commits, rebases, or base changes, re-evaluate the complete range and
refresh the title, body, verification, and risk notes that became stale. Before
handoff, report the pull-request URL and any checks, conflicts, review requests,
or local changes that still require attention.
