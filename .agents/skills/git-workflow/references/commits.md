# Construct coherent commits

## Choose the boundary

A commit records one reason for change. Partition a mixed working tree by
behavioral or operational intent, not by file type or directory:

- Keep code, tests, contracts, public documentation, migrations, and generated
  output together when they are required to deliver or explain the same
  outcome.
- Separate an independently useful refactor, behavior change, documentation
  correction, build change, or cleanup when it can be understood and verified
  on its own.
- Put enabling work before the change that depends on it. Combine groups whose
  separation would create a misleading, broken, or unverifiable intermediate
  commit.
- Leave unrelated pre-existing changes unstaged. When one file contains more
  than one intent, stage by hunk and re-read both staged and unstaged views.

Before each commit, `git diff --cached` must tell one complete story and
`git diff --cached --check` must pass. Run the narrow verification appropriate
to that story. When no safe boundary exists inside an intertwined working tree,
stop before committing and describe the overlap precisely.

## Write the message

Use the repository's Conventional Commit-style subject:

```text
<type>[(<scope>)]: <specific imperative outcome>
```

Choose the type that describes the primary intent:

- `feat` for new behavior;
- `fix` for corrected behavior;
- `refactor` for a structural change intended to preserve behavior;
- `docs` for documentation only;
- `test` for test-only work;
- `perf` for a measured performance improvement;
- `build` for build or dependency mechanics;
- `ci` for automation pipelines; and
- `chore` for necessary maintenance that fits none of the preceding types.

Use the narrowest stable owner as the optional scope, such as `server`,
`webapp`, `docs`, `cache`, `mail`, `vfs`, or `tooling`. Omit the scope when the
change is genuinely repository-wide or a narrower owner would mislead.

Write the subject in lowercase imperative form, without a trailing period, and
keep it specific enough to distinguish the commit from neighboring history.
Aim for 72 characters or fewer. Prefer an outcome such as
`fix(server): reject expired desktop claims` over a file inventory or a vague
activity such as `update auth files`.

Add a body when the reason, non-obvious mechanism, compatibility effect,
security consequence, migration path, or operational tradeoff is not clear
from the subject and diff. Explain why the change takes this shape and what a
reviewer must understand; let the diff enumerate files and mechanics. Use
footers for issue references, co-authorship, or other repository metadata.
Mark an intentional incompatible contract with `!` in the subject and a
`BREAKING CHANGE:` footer that explains the required transition.

## Create or revise commits

Stage only the explicit paths or hunks belonging to the current group, then
compare the index with `HEAD`. Create the commit using the repository's active
identity, hooks, and signing configuration. Confirm the recorded author,
subject, body, and diff after creation.

Amend, fix up, squash, reorder, or split commits only within the history the
user authorized. First determine whether the commits are already published or
shared. Preserve authorship and meaningful message context while rewriting,
and show the resulting range before any remote update.
