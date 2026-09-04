---
name: git-workflow
description: Maintain coherent Git history when planning or performing commits, branch or history edits, pushes, tags, or pull requests.
---

# Maintain coherent Git history

## Workflow

1. Establish the exact repository state and authority for the requested
   operation. Inspect the working tree, current branch, relevant refs, remotes,
   and the smallest useful diff or log range. Distinguish the user's existing
   work from changes made for the current task. Completion: the base, target,
   owned changes, and authorized mutation are unambiguous.
2. Partition the work into coherent review units before staging or publishing
   it. Assign every selected hunk to exactly one intent; keep the implementation,
   tests, contracts, documentation, and generated artifacts for that intent
   together. Order dependent units so each leaves the repository in a useful,
   verifiable state. Completion: every selected change has one logical group
   and unrelated changes remain outside it.
3. Read the reference for each operation before executing it:
   - For staging, committing, amending, fixups, or squashing, read
     [commit construction](references/commits.md).
   - For opening, updating, or preparing a pull request, read
     [pull requests](references/pull-requests.md).
   - For branches, rebases, merges, restores, pushes, tags, or other ref and
     remote work, read [history and remotes](references/history-and-remotes.md).
   Completion: every applicable reference has been applied to the exact diff
   and refs in scope.
4. Verify the resulting state. Re-read the staged or published diff, run the
   checks appropriate to each logical group, and inspect the final status and
   concise history. Completion: the result contains only intended changes,
   names the verification actually run, and reports any remaining local or
   remote work without mutating it.

## Operating boundaries

- Treat read-only inspection as permission to inspect, not permission to
  commit, rewrite, push, tag, open a pull request, or otherwise change shared
  history. Perform the mutation the user requested and leave adjacent Git
  operations for separate authorization.
- Preserve local modifications, untracked files, staged state, refs, and
  reflog recovery paths that are outside the requested operation.
- Use explicit pathspecs, hunks, branches, commits, and remotes. Resolve each
  target from repository evidence before changing it.
- Respect repository hooks and signing configuration. Report a failing gate;
  bypass it only when the user explicitly requests that tradeoff.
