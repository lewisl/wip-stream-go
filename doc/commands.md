# Go command guide

Start with [the everyday workflow](../README.md). This guide explains the CLI
options and the recovery commands in more detail.

## Select a repository and get help

Every command operates on the clone in the current directory unless you provide
`-C` or `--repo`:

```sh
wipstream -C /path/to/project get
wipstream --help
wipstream save --help
```

Global options:

| Option | Meaning |
| --- | --- |
| `-C PATH`, `--repo PATH` | Select the target Git clone. |
| `--json` | Print a successful result as JSON. Errors still go to standard error. |
| `-h`, `--help` | Show command help. |

Branch selection uses your normal Git client, for example `git switch my-task`.

## init

Initialize each independent clone. Successful setup synchronizes ordinary
branches and checks out the remote default branch. Files already saved to disk
are authoritative; the app does not save editor buffers.

```sh
wipstream init
wipstream init --remote upstream --authority local-work -m "Setup checkpoint"
```

| Option | Meaning |
| --- | --- |
| `--remote NAME` | Git remote to use. Defaults to the existing WipStream selection, otherwise `origin`. An initialized clone refuses a conflicting selection. |
| `--authority local-work` | Checkpoint changes and synchronize safe advances, including unpublished local branches. |
| `--authority remote` | Replace local branches and files with the remote's state. Requires backup or explicit discard approval. |
| `--authority reconcile` | Stop and preserve work so you can resolve branch histories locally before retrying setup. |
| `--authority cancel` | Stop and preserve work. |
| `--backup-parent PATH` | Existing parent directory outside the project for a verified complete backup before remote replacement. |
| `--discard-local-work` | Explicitly approve remote replacement without a backup. |
| `-m MESSAGE`, `--message MESSAGE` | Message for a local-work checkpoint when files need committing. |

A clean matching clone needs no authority decision. If a decision is needed,
an interactive terminal offers the choices above. Noninteractive runs require
an explicit `--authority`.

For remote authority, tracked files are replaced, non-ignored untracked files
and local-only branches are removed, and ignored files are preserved. Collisions
with remote paths block replacement. This path does not push or create a content
commit.

Create a backup before replacement:

```sh
wipstream init --authority remote --backup-parent /existing/backup-parent
```

The new backup includes the self-contained project and Git history; its path is
reported after successful replacement. It is an ordinary folder you can inspect.
Copy desired changes back and Save to publish them. Remote replacement is not
eligible for Undo, and WipStream does not automatically restore or delete backups.

When deliberately replacing work without a backup, use:

```sh
wipstream init --authority remote --discard-local-work
```

## get

Retrieve safe remote changes across all ordinary branches before editing here:

```sh
wipstream get
```

Get imports remote-only branches, fast-forwards safe local branches, and removes
branches that were safely deleted remotely. It refuses uncommitted files,
unpublished local commits or branches, divergent histories, and deletions whose
local branch advanced since the previously observed remote tip. Resolve those
conditions before retrying; Get does not discard local work to make progress.

## save

Save editor files, then checkpoint and publish:

```sh
wipstream save -m "Checkpoint the parser changes"
```

Save stages all non-ignored additions, edits, and deletions and honors Git commit
hooks. `-m` supplies the checkpoint message; without it, an interactive terminal
asks when there is content to commit. A blank message is refused. If no new
checkpoint is needed, existing safe local commits can still be published.

Save synchronizes all ordinary branches, including safe local-only branches and
unrelated remote advances. Publication must complete before resuming in another
clone. A retained local checkpoint after a failed Save is not a completed handoff.

## start

Create and check out a new task branch from the current branch:

```sh
wipstream start parser-cleanup
```

The current branch becomes the task's recorded parent. Uncommitted work carries
into the new branch without a commit or push. Omit the branch name to enter it
interactively. Existing local or fetched remote names are refused.

## update

Bring the recorded parent's history into the task branch:

```sh
wipstream update
# For an imported branch with no recorded parent:
wipstream update --parent main
```

Update first performs Get, so files must be clean and local work already
published. It stops if retrieval changes the intended checkout. If the branch
already contains its parent, no merge is necessary. Otherwise it merges the
parent, preserving a pending merge if conflicts occur.

A clean Update leaves the merge local. Run Save afterward, or let Finish perform
the handoff. A conflicted Update uses Continue or Abort as described below.
`--parent` supplies the parent when the branch has no recorded parent; it does
not override a parent recorded by Start.

## finish

Incorporate a completed task into its parent, then check out the parent:

```sh
wipstream finish --disposition retain
# Or delete the completed branch locally and remotely:
wipstream finish --disposition delete
```

An interactive run can ask for the disposition and confirmation. For a script:

```sh
wipstream finish --disposition delete -m "Final checkpoint" --yes
```

The work branch must contain its parent's history. If the parent advanced
independently, Update first. Finish previews the relationship before saving;
cancelling does not checkpoint or publish. After approval it runs Save, advances
the parent locally and remotely, and retains or deletes the task branch as chosen.
Deletion also removes that branch's configuration.

Use `--parent NAME` for an imported branch without recorded parent metadata.
`-m` supplies a message if Finish's Save needs a checkpoint.

## reconcile

Merge the checked-out branch's divergent remote history, then Save:

```sh
git switch my-task
wipstream reconcile
```

The clone must be initialized, the files clean, and the selected branch actually
diverged. Reconcile refuses when another branch also diverges; resolve that
branch history in your Git client first. A merge may succeed even when the file
trees are identical, because history and file conflicts are different conditions.

Use Continue or Abort if conflicts leave the merge pending. `-m MESSAGE` can
supply a checkpoint message if the subsequent Save needs one; Git's recorded
merge message is used for the merge itself.

## continue

Resolve each conflict, save the file, and mark it resolved with Git:

```sh
git add path/to/resolved-file
wipstream continue
```

Continue requires exactly one pending WipStream merge and no unresolved index
conflicts. It verifies the branch and merge target, stages current changes,
commits using Git's merge message, and runs Save. Review all files before running
it because its staging includes other non-ignored changes too.

If an external Git client already completed the merge, WipStream recognizes it
when its recorded evidence proves the outcome. If the outcome cannot be proved
and Git has no active operation, use Recover. `-m MESSAGE` applies only if the
subsequent Save needs a checkpoint.

## abort

Abort the pending WipStream merge:

```sh
wipstream abort
```

Abort verifies restoration of the recorded branch, commit, index, and worktree
state before reporting success. It applies to a pending merge from Update or
Reconcile. It does not reset a completed merge or abort an unrelated Git
operation. An unverifiable outcome leaves the receipt incomplete for inspection
and recovery.

## recover

Close a stopped WipStream attempt while keeping current state:

```sh
wipstream recover
# Select one of several incomplete attempts using its reported ID:
wipstream recover --operation OPERATION_ID --yes
```

When exactly one incomplete attempt exists, its ID can be omitted. When several
exist, the error lists IDs, commands, and phases; choose with `--operation`.
An interactive run asks for confirmation. Noninteractive runs need `--yes`.

Recover preserves files, staged and untracked work, commits, refs, and recovery
history. It does not replay the old plan, push, undo, or certify a handoff.
Complete or abort active Git operations first. After recovery, retry the
appropriate normal command from the retained state; resolve every remaining
incomplete attempt before retrying.

Recover can also archive a provably stale command lock left by a dead local
process. It refuses active or unverifiable owners. Windows owners are currently
treated conservatively as unverifiable for stale-lock reclamation.

If remote replacement stopped partway through, retain its backup and inspect
current state. Recover the attempt, select the intended existing branch with
Git if the checkout is detached, then rerun Init for a fresh decision. Do not
edit receipt files or private refs to force a retry.

## undo

Reverse the latest eligible completed WipStream action:

```sh
wipstream undo
# Explicit approval for a script:
wipstream undo --yes
```

Eligible action classes are Get, local-work Init, Save, Finish, Update, and
Condense. Undo requires the recorded checkout, files, relevant configuration,
local refs, and fetched remote state to match the completed action. Later edits,
commits, branch changes, or remote changes can make it ineligible.

Undo of Save can restore checkpointed work as uncommitted changes. Undo of Finish
restores the task and parent branch state, including a branch deleted by Finish.
Remote replacement and recovered attempts are not eligible. Undo itself is not
an action you can repeatedly undo to walk backward through history.

## condense

Replace commits exclusive to the task branch with one commit containing the
same final files:

```sh
wipstream save -m "Last checkpoint before condensing"
wipstream condense -m "Complete the parser cleanup"
# Explicit approval for a script:
# wipstream condense -m "Complete the parser cleanup" --yes
```

Condense requires clean files, matching local/remote branch state, a parent that
is an ancestor of the task branch, and at least two commits exclusive to the task.
It asks for confirmation and a nonblank commit message. `--parent NAME` supplies
an imported branch's unrecorded parent.

It rewrites and publishes the task branch while preserving its final tree. Use
one editing clone at a time. A different clone holding the old checkpoints may
need history reconciliation; Get will preserve that old history and refuse an
unsafe replacement.

## Noninteractive runs and results

Normal terminal output starts with `SUCCESS` and describes the verified result:
checkpoint creation, branches pushed or retrieved, and the final checkout as
applicable. Commands that make no changes say so. A merge requiring attention
starts with `PENDING` and lists the conflicts and the Continue/Abort commands.

Routine completion messages omit operation IDs. These IDs identify local
WipStream records, not Git commits; you do not need them for Undo. When an
interrupted operation needs recovery, an error can show its ID and the command
that uses it. Verified backup paths are still reported after remote replacement.

Supply inputs explicitly when running without a terminal:

```sh
wipstream -C /path/to/project --json save -m "Checkpoint before switching"
wipstream -C /path/to/project --json finish --disposition retain -m "Final checkpoint" --yes
```

`--yes` is available on Finish, Condense, Undo, and Recover. It approves the action
without prompting, while keeping all repository-state checks. Init's remote
replacement approval uses `--backup-parent` or `--discard-local-work` instead.

Successful results can contain `message`, `operationId`, `checkout`,
`checkpointCreated`, `published`, `pending`, and `conflicts`. Empty strings and
false optional fields are omitted. A pending merge returns success with
`pending: true`; that still needs Continue or Abort. `published` describes the
workflow result: Get, Start, clean Update, Undo, and Recover do not claim a Save
handoff. Remote authority Init reports adoption without publication.

JSON results retain `operationId` separately from `message` and omit the
terminal's status prefix. Errors return a nonzero exit status and appear with
`ERROR` on standard error, even with `--json`. A `WARNING` can describe retained
local work after a failed handoff, including an Init checkpoint or a completed
merge whose subsequent push failed. Check the exit status and pending state
before treating a run as a completed handoff.
