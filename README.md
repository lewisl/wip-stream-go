# WipStream Go

WipStream helps one person save work in progress and continue it on another
computer. Work on a named branch, run `wipstream save` before leaving a computer,
and run `wipstream get` before editing on the next one.

Use one editing clone at a time. Keep completed work on `main` (or your remote's
default branch), and develop on work branches. Committing directly on `main` is
also supported.

The Go app is a standalone command-line implementation of
[the WipStream VS Code extension](https://github.com/lewisl/wip-stream).
Both can operate alternately on the same initialized clone. The Go executable
requires Git at runtime; it does not require VS Code, Node.js, or Go.

## Install the Go app

Build from source with Go 1.27.1 or later and Git. The implementation currently
lives on the `go-cli-roundtrip` branch:

```sh
git clone --branch go-cli-roundtrip https://github.com/lewisl/wip-stream-go.git
cd wip-stream-go
go build -o bin/wipstream ./cmd/wipstream
export PATH="$PWD/bin:$PATH"
wipstream --help
```

The `export` adds the executable to this shell's PATH. For later sessions, put
`wipstream` in a directory already on your PATH or add its installation directory
to your shell configuration. On Windows, build with
`go build -o bin/wipstream.exe ./cmd/wipstream` and add that directory to PATH.
Current runtime validation has been performed on Linux.

This is an initial compatibility implementation. See the
[validation record](design/validation.md) for tested behavior and platforms.

## Set up each computer

1. Use a separate, ordinary, complete Git clone on each computer, with a
   configured remote, usually `origin`. Linked Git worktrees and shallow clones
   are refused.
2. Configure your Git commit identity and remote access as you would for normal
   Git use. WipStream uses Git's existing credentials and honors commit hooks.
3. Save your editor's files to disk, then run `wipstream init` inside each clone.
   Successful initialization checks out the remote default branch. A clone
   already initialized by the extension can be used directly by the Go app.
4. Start new work with `wipstream start`, or select an existing branch with
   `git switch` or another Git client.

```sh
cd /path/to/project
wipstream init
```

Use `wipstream -C /path/to/project init` to select a clone without changing the
shell's working directory. For a remote other than `origin`, pass
`wipstream init --remote upstream`.

The remote must identify a default branch, support atomic pushes, and allow the
branch creation, updates, and deletions your workflow uses. Stop any folder synchronization
service from modifying these clones; do not synchronize their `.git` directories.

### Choose which work to keep

Initialize inspects files and all ordinary branches, including those in a copied
project that already carries WipStream configuration. A clean matching clone
can finish without extra decisions. Otherwise it asks which work to keep:

| Choice | CLI option | Result |
| --- | --- | --- |
| Keep this computer's work and save it to the remote | `--authority local-work` | Checkpoint non-ignored changes and publish safe local commits and local-only branches. Divergence retains the checkpoint locally and prevents publication. |
| Use the remote's version | `--authority remote` | Replace tracked files and ordinary local branches to match the remote; remove local-only branches and non-ignored untracked files. Preserve ignored files; refuse collisions with remote paths. |
| Resolve differences locally, then retry | `--authority reconcile` | Stop while preserving work. Incorporate the named remote histories in your Git client, then rerun initialization with `local-work`. |
| Cancel | `--authority cancel` | Stop while preserving work. |

For example, keep and publish this computer's work:

```sh
wipstream init --authority local-work -m "Checkpoint before setup"
```

Before replacing local work with the remote's version, select an existing backup
parent outside the project:

```sh
wipstream init --authority remote --backup-parent /path/to/backups
```

WipStream creates and verifies a new complete project copy, including `.git`,
ignored files, and untracked files, and reports its path. Inspect it separately
and copy desired changes back before saving. The backup is not restored or
published automatically. Remote replacement refuses submodule checkouts and
projects whose external Git storage cannot be safely backed up.

Replacing without a backup requires explicit `--discard-local-work` approval.
Remote replacement cannot be reversed with `wipstream undo`.
See [the command guide](doc/commands.md#init) for the options.

## Start a new task

Choose the branch you eventually want the work incorporated into, then start a
branch with a descriptive task name:

```sh
wipstream get
git switch main
wipstream start histories-rewrite
```

| Branch checked out before Start | New task branch's parent | Finish incorporates work into |
| --- | --- | --- |
| `main` | `main` | `main` |
| `feature` | `feature` | `feature` |

Start switches to the new branch and carries staged, unstaged, and untracked
changes without committing or publishing them. Later commits advance the task
branch. An existing local or fetched remote branch name is refused.

Use `git switch histories-rewrite` or another Git client to switch between
existing branches. WipStream has no general branch-switching command.

## Save work without finishing the task

Save your editor's files, then checkpoint and publish:

```sh
wipstream save -m "Implement the first part"
```

Repeat Save whenever useful while the task is unfinished. Without `-m`, an
interactive run asks for a message when there are changes to commit. A clean
Save publishes existing safe commits without creating an empty commit.

Save stages all non-ignored additions, changes, and deletions, including work
you had not staged yourself. It includes commits made in another Git client
and synchronizes safe advances across **all ordinary branches**. Ignored files
and empty directories are not included; dirty submodules must be handled first.

The Go app reads files on disk. Save unsaved editor buffers yourself before
running it.

**Wait for Save to succeed before continuing on another computer.** A local
checkpoint can succeed while publication fails. If Save returns an error,
keep working in this clone and resolve the reported cause before moving on.

## Continue on another computer

1. On computer A, save editor files and run `wipstream save`. Wait for success.
2. On computer B, run `wipstream get` in its existing clone **before editing**.
   Initialize first if this is a new clone.
3. Select the intended work branch with `git switch` if necessary, then work.
4. Before returning to A, run Save on B; run Get on A before resuming there.

```sh
# Computer B, after a successful Save on computer A:
cd /path/to/project
wipstream get
git switch histories-rewrite
```

Get retrieves all ordinary branches, including new branches and safe deletions.
It normally preserves the checkout. If that branch was safely deleted remotely,
it selects its recorded parent or the remote default branch. It refuses dirty
files, unpublished local work, divergence, and unsafe deletions.

You can switch computers while a task is unfinished. Finish is for incorporating
completed work into its parent.

## Finish a completed task

Check out the completed task branch, then choose whether to retain it or delete
it locally and remotely:

```sh
git switch histories-rewrite
wipstream finish --disposition retain
# Alternatively, choose deletion:
# wipstream finish --disposition delete
```

Finish asks for confirmation, runs Save, advances the parent to the completed
branch's commit locally and remotely, and checks out the parent. Cancelling the
preview does not checkpoint or publish work. For a branch created outside
WipStream, confirm its parent when asked or supply `--parent main`.

If the parent advanced independently, run `wipstream update`, resolve any merge
conflicts, then save and retry Finish. A clean Update creates a local merge;
run Save to publish it. Finish can also perform that handoff itself.

A task started from `feature` finishes into `feature`; finishing `feature` into
`main` is a separate action. After finishing, start a new task before editing.

## Use the extension or another Git client

You can use the Go app, VS Code WipStream, Fork, and Git in the same clone.
Avoid simultaneous Git mutations. A normal local commit can be published by
`wipstream save`.

Amending or rebasing published commits can make local and remote histories
diverge even if the files look identical. If Save reports divergence, select the
named branch and run `wipstream reconcile` with a clean working directory.
Reconcile merges remote history and saves the result. If another branch also
diverges, resolve that history in your Git client first.

## When something stops

Read the terminal error and any reported operation ID. A pending merge is
reported as pending even when the command exits successfully; complete or abort
it before starting another workflow.

| Situation | What to do |
| --- | --- |
| A WipStream merge has conflicts | Edit the files to the desired contents, stage resolved paths with `git add`, then run `wipstream continue`. Use `wipstream abort` to restore the recorded pre-merge state. |
| You completed or aborted the merge in another Git client | Run Save. WipStream closes the stale record when the resulting Git state proves what happened. |
| Git has no active operation, but WipStream reports an incomplete attempt | Run `wipstream recover`, confirm keeping current state, then retry the appropriate normal command. For several attempts, select each with `--operation ID`. |
| Git has an unrelated rebase, cherry-pick, or merge | Complete or abort it in your Git client first. |
| Publication failed | Keep the local checkpoint. Address connectivity, permissions, or history divergence; recover a blocking incomplete attempt if needed, then retry Save. |
| Get refuses unpublished local work | Save that work first, or resolve the reported branch history in your Git client. |
| A command lock remains after a process stopped | Recover can reclaim a provably stale lock on this machine. Active or unverifiable owners remain blocked. Windows stale-lock reclamation is currently conservative. |

Continue commits the resolved merge and runs Save. Abort verifies restoration
before reporting success. Recover preserves current files, staged changes,
untracked files, commits, and refs; it closes the selected attempt without
publishing or certifying a handoff. Do not edit or delete operation receipts
manually.

## Commands at a glance

| Go command | Corresponding extension command | Purpose |
| --- | --- | --- |
| [init](doc/commands.md#init) | Initialize Repository | Set up a clone and choose authoritative work. |
| [get](doc/commands.md#get) | Get from Remote | Retrieve all branches before resuming here. |
| [save](doc/commands.md#save) | Commit and Save | Checkpoint files and synchronize all branches. |
| [start](doc/commands.md#start) | Start Branch | Begin a task from the current branch. |
| [finish](doc/commands.md#finish) | Finish Branch | Incorporate completed work into its parent. |
| [update](doc/commands.md#update) | Update from Parent | Bring the parent's history into the task branch. |
| [reconcile](doc/commands.md#reconcile) | Reconcile with Remote | Merge divergent local and remote histories and save. |
| [continue](doc/commands.md#continue) | Continue Pending Merge | Commit a resolved WipStream merge and save. |
| [abort](doc/commands.md#abort) | Abort Pending Merge | Restore the recorded pre-merge state. |
| [recover](doc/commands.md#recover) | Recover Incomplete Operation | Keep current state and close a stopped attempt. |
| [undo](doc/commands.md#undo) | Undo Last Action | Reverse the latest eligible completed action, provided no later state changes prevent it. |
| [condense](doc/commands.md#condense) | Condense Branch (Advanced) | Replace branch-only commits with one commit preserving the final files. |

For scripting, use explicit inputs and `--yes` on commands that require
confirmation. `--yes` approves the action; it does not bypass repository safety
checks. `--json` prints structured results. See
[CLI options, examples, and recovery guidance](doc/commands.md).

## Documentation

End-user guides live in [`doc/`](doc/commands.md). Developer and contributor
material lives in `design/`:

- [Go tests and interoperability checks](design/testing.md)
- [Recorded validation results](design/validation.md)
- [Port specification](design/overall_spec.md)
- [Command semantics](design/command_spec.md)
- [Shared persistence protocol](design/persistence_protocol.md)

Contributors should read [AGENTS.md](AGENTS.md) and the port specification
before changing behavior or persistent state.
