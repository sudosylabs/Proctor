# Manage history and remotes

## Branches and worktrees

Resolve the starting commit before creating or switching branches. Use an exact
user-supplied branch name; otherwise use the configured `feat/` prefix with a
short kebab-case purpose. Confirm how staged, modified, and untracked files will
move before changing checkout or worktree state, and preserve work that does
not belong to the requested branch operation.

Delete a branch or worktree only after resolving its exact path or ref,
checking merge and publication state, and establishing the requested recovery
expectation.

## Rebases, merges, and restoration

Inspect both sides of a rebase or merge and determine whether the affected
history is private or shared. Use the strategy the user requested; when none is
specified, preserve the repository's observed history shape and explain any
choice that changes commit identity or introduces a merge commit.

Before restoring files, resetting refs, cleaning untracked content, or dropping
commits, identify the exact affected paths and commits and show the recoverable
source. Prefer a new commit for corrections to shared history. Preserve a
reflog or named ref recovery path for an authorized local rewrite until the
result has been verified.

Resolve conflicts by preserving the intended behavior from both sides rather
than mechanically choosing one version. Re-run the verification belonging to
the combined change, inspect the final diff, and continue the Git operation
only when every conflict marker and unresolved entry is gone.

## Remotes and pushes

Resolve the remote URL, upstream branch, and expected remote tip before a
push. Publish the explicit local ref to the explicit remote ref. When ordinary
push protection rejects rewritten history, report the divergence. Use
`--force-with-lease` only for an authorized rewrite after refreshing and
checking the remote tip; treat a lease failure as new evidence to inspect.

Fetching remote refs does not authorize merging, rebasing, deleting, or
publishing them. Leave unrelated tracking refs and remote configuration
unchanged.

## Tags

Resolve and display the target object before creating a tag. Use an annotated
tag for a release or other durable milestone, with a message that identifies
the milestone. Verify the tag object locally before publishing it. Moving,
replacing, or deleting a local or remote tag requires explicit authorization
for that exact tag and target.
