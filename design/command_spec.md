# WipStream Command Behavior Specification

## 1. Purpose

This document specifies the behavior of the public `wipstream` commands.

It supplements `design/overall_spec.md`. The overall specification defines the architecture, compatibility requirements, persistent protocol, testing strategy, and implementation constraints. This document defines the user-visible and repository-visible semantics of each public command.

The existing TypeScript WipStream implementation remains authoritative for edge cases not fully described here.

The Go implementation should reproduce the same repository state transitions and safety guarantees while adapting editor-specific interaction to a terminal CLI.

## 2. Command Surface

The initial Go implementation exposes:

```text
wipstream init
wipstream get
wipstream save
wipstream start
wipstream finish
wipstream update
wipstream reconcile
wipstream continue
wipstream abort
wipstream recover
wipstream undo
wipstream condense
```

These correspond directly to the existing WipStream commands:

| CLI | Existing command |
| --- | --- |
| `init` | Initialize Repository |
| `get` | Get from Remote |
| `save` | Commit and Save |
| `start` | Start Branch |
| `finish` | Finish Branch |
| `update` | Update from Parent |
| `reconcile` | Reconcile with Remote |
| `continue` | Continue Pending Merge |
| `abort` | Abort Pending Merge |
| `recover` | Recover Incomplete Operation |
| `undo` | Undo Last Action |
| `condense` | Condense Branch |

The Go implementation must not add generic Git functionality to this command set merely for convenience.

## 3. Common CLI Conventions

### 3.1 Repository selection

The CLI operates on the Git repository containing the current working directory.

Typical use is therefore:

```text
cd ~/code/epi_sim
wipstream save
```

The initial implementation does not require a repository path argument or repository picker.

Repository discovery should use Git rather than assuming the current directory itself is the repository root.

### 3.2 Filesystem state is authoritative

The VS Code implementation can inspect and save editor buffers. The standalone CLI cannot and must not depend on VS Code or another editor.

The Go implementation therefore operates solely on filesystem and Git state.

Users are responsible for saving editor buffers before invoking commands when unsaved editor state matters.

The CLI must still detect changes to files, refs, configuration, index state, and other observable repository state that occur while an interactive command is in progress.

It must not attempt to discover or control unsaved editor buffers.

### 3.3 Git execution

All Git behavior is performed through the installed `git` executable.

Commands should preserve the TypeScript implementation's important distinctions between:

- inspection;
- checkpoint creation;
- fetch;
- remote publication;
- local ref transaction;
- checkout changes;
- configuration changes;
- merge;
- recovery;
- final verification.

A successful individual Git subprocess is not sufficient proof of a successful WipStream command.

### 3.4 Command locking

Mutating workflows use the existing WipStream command-lock protocol.

A second WipStream mutation must not run concurrently against the same clone.

Stale-lock recovery must remain compatible with the TypeScript implementation.

### 3.5 Incomplete operations

Normal workflows refuse to proceed when an incompatible incomplete WipStream operation exists.

Pending WipStream merges are handled through `continue` or `abort`.

Other incomplete operations are handled through `recover`.

### 3.6 Branch scope

Unless a command explicitly concerns only one branch, WipStream reasons about all ordinary branches.

Private `refs/wipstream/*` refs are not ordinary branches.

### 3.7 Exact expected state

Remote rewrites and updates use exact expected prior values.

Local compound ref changes use expected-old values and transactional Git ref updates.

The Go implementation must not replace these protections with unconditional force operations.

---

# 4. `wipstream init`

## 4.1 Purpose

Initialize the current clone for WipStream and establish which copy of the work is authoritative.

Initialization may be performed on a clean clone or on a clone containing:

- uncommitted ordinary files;
- staged changes;
- existing local commits;
- local-only branches;
- differing remote history.

Initialization concerns all ordinary branches, not merely the checked-out branch.

## 4.2 Remote selection

If the repository is not already initialized, prompt for the Git remote, defaulting to:

```text
origin
```

The remote name must:

- be non-empty;
- exist in the repository.

If the repository is already initialized, use its configured `wipstream.remote`.

An initialized clone must still be fully inspected. Existing WipStream configuration does not imply that current files or histories are safe.

## 4.3 Inspection

Before asking the user to choose authority, inspect at least:

- repository shape;
- active Git operations;
- conflicts;
- incomplete WipStream operations;
- configured remote;
- checked-out branch;
- local ordinary branches and tips;
- pre-fetch remote-tracking tips;
- fetched remote branches;
- server remote default branch;
- branch relationships;
- working-tree state;
- index state;
- relevant local Git configuration;
- filesystem state needed to detect stale approval.

Dirty ordinary files are allowed during initialization inspection.

Unsupported repository layouts remain refused according to the reference implementation.

## 4.4 No-choice initialization

If local state already safely matches the fetched remote state and no authority decision is required, initialization should complete without presenting an unnecessary authority menu.

The clone should finish configured consistently with the existing implementation.

## 4.5 Authority choice

When local and remote state require a decision, present the equivalent of:

```text
1. Use the remote's version
2. Commit this machine's work and save to remote
3. Resolve differences locally, then save to remote
4. Cancel
```

The UI may use `huh` selection rather than literal numeric input, but the semantic choices must remain the same.

## 4.6 Local-work path

For:

> Commit this machine's work and save to remote

the command:

1. Revalidates that the approved repository state has not changed.
2. Stages all ordinary additions, modifications, and deletions.
3. If staged changes exist, requests a checkpoint commit message.
4. Creates a normal Git commit, honoring commit hooks.
5. Retains the checkpoint locally even if later publication cannot safely complete.
6. Fetches the selected remote again.
7. Reclassifies all ordinary branches.
8. If any branch has unresolved divergence or an unsafe remote deletion, publishes nothing.
9. Otherwise publishes safe local-only and local-ahead histories using exact leases and one atomic remote publication.
10. Refetches and verifies publication.
11. Applies unrelated safe remote advances locally.
12. Establishes branch tracking configuration.
13. Records the selected WipStream remote.
14. Updates the cached remote default ref as required.
15. Finishes on the remote default branch.
16. Verifies complete expected repository state before reporting success.

If divergence prevents publication, any newly created checkpoint remains local.

The result must make clear that remote handoff did not complete.

## 4.7 External-reconciliation path

For:

> Resolve differences locally, then save to remote

the command does not attempt to merge histories.

It preserves existing work and identifies the branches that require reconciliation.

The user resolves those histories in a Git tool and then reruns:

```text
wipstream init
```

and chooses the local-work path.

Matching file contents alone do not constitute reconciliation; ancestry must be reconciled.

## 4.8 Remote-authority path

For:

> Use the remote's version

the command must require either:

- a verified complete project backup; or
- explicit confirmation that replacement may proceed without a backup.

For a backup, the CLI should prompt for a destination parent directory rather than depending on a graphical folder picker.

The backup semantics must match the TypeScript implementation: it is an ordinary, independently inspectable copy preserving the complete project state that the existing backup implementation preserves.

Remote adoption then:

1. Revalidates the approved state.
2. Refuses unsupported replacement conditions such as those defined by the reference implementation.
3. Records the replacement operation.
4. Removes only approved non-ignored untracked entries.
5. Replaces tracked state with the exact fetched remote state.
6. Replaces ordinary local branch refs with the approved remote branch state.
7. Removes local branches absent from the remote where required.
8. Creates recovery refs for displaced reachable commits.
9. Preserves ignored files as required by the existing implementation.
10. Configures branch tracking and WipStream initialization state.
11. Checks out the remote default branch.
12. Refetches and verifies that the remote did not change unexpectedly.
13. Verifies local/remote branch parity and final working-tree state.

This path:

- does not push;
- does not merge;
- does not create a content commit.

Remote adoption is not eligible for ordinary Undo because restoring refs cannot restore discarded uncommitted filesystem state.

## 4.9 Cancellation and stale approval

Cancellation before mutation leaves work unchanged.

If repository state changes after preview or approval, the previous approval must not silently carry forward.

The command must require a fresh inspection/decision where the TypeScript implementation does.

In particular, approval to discard local work must never be reused after the state being discarded changes.

---

# 5. `wipstream get`

## 5.1 Purpose

Retrieve remote WipStream state before resuming work in the current clone.

It synchronizes safe remote changes across all ordinary branches.

## 5.2 Preconditions

The repository must:

- be initialized for WipStream;
- have an ordinary checked-out branch;
- have a clean working tree;
- have no unresolved conflicts;
- have no incompatible active Git operation;
- have no blocking incomplete WipStream operation;
- satisfy the ordinary repository-layout requirements.

## 5.3 Behavior

The command:

1. Reads the configured WipStream remote.
2. Snapshots previously observed remote-tracking tips.
3. Fetches and prunes all ordinary remote branches.
4. Resolves the remote default branch.
5. Classifies every ordinary branch relationship.
6. Refuses automatic retrieval if there is unpublished local work.
7. Refuses divergent histories.
8. Refuses ambiguous remote deletion where the local branch changed after its last observed remote state.
9. Plans exact local ref changes.
10. Creates local branches for remote-only branches.
11. Fast-forwards local branches for safe remote advances.
12. Deletes safely proven remotely deleted local branches.
13. Preserves the current checkout whenever possible.
14. If the current branch was safely deleted remotely, chooses a surviving recorded parent when possible, otherwise the remote default branch.
15. Configures tracking for newly created local branches.
16. Verifies complete expected branch parity.
17. Produces parent-status advisories as in the reference implementation.
18. Records and completes the operation receipt.

## 5.4 Refusal principle

`get` must never solve unpublished or divergent local history by silently merging, rebasing, resetting, or overwriting it.

The user must explicitly address that state through the appropriate WipStream or Git workflow.

---

# 6. `wipstream save`

## 6.1 Purpose

Checkpoint current work and perform a complete remote handoff.

This is the primary operation used before moving work to another machine.

## 6.2 Preconditions

The repository must be initialized and on an ordinary branch.

Dirty ordinary files are expected and allowed.

Dirty or otherwise unsafe submodule state is refused according to the reference implementation.

Active incompatible Git operations, unresolved conflicts, and blocking incomplete WipStream operations are refused.

## 6.3 Checkpoint creation

The CLI does not save editor buffers.

It:

1. Treats files currently present on disk as authoritative.
2. Stages all ordinary additions, modifications, and deletions with Git.
3. If staged content exists, prompts for a commit message.
4. Creates a normal checkpoint commit.
5. Honors normal Git commit hooks.
6. Does not create an empty content commit merely because `save` was invoked.

Existing commits created by another Git client are included in the synchronization operation even when no new checkpoint is needed.

## 6.4 Synchronization

After checkpoint handling, `save`:

1. Snapshots previous remote-tracking state.
2. Fetches and prunes all branches.
3. Classifies every ordinary branch.
4. Detects divergence and unsafe remote deletion.
5. If unsafe history exists, retains the local checkpoint but publishes nothing.
6. Plans remote publication for local-ahead and local-only branches.
7. Uses exact expected remote values.
8. Publishes all planned remote branch changes atomically.
9. Refetches.
10. Verifies publication.
11. Applies safe unrelated remote advances to local branches.
12. Creates local branches that appeared remotely where appropriate.
13. Applies safe remote deletions.
14. Preserves the checkout or safely changes it when required by the established workflow.
15. Verifies complete branch parity.
16. Completes the operation receipt.

## 6.5 Local checkpoint versus completed handoff

Creating a local commit is not equivalent to completing a remote handoff.

If network access, remote movement, branch divergence, cancellation, or another synchronization problem prevents publication, the command must clearly report:

- the local checkpoint remains;
- the remote handoff is incomplete;
- the user should not resume the work from another clone yet.

This distinction is part of WipStream's core semantics.

---

# 7. `wipstream start`

## 7.1 Purpose

Create a new work branch from the currently checked-out branch and remember that branch as its WipStream parent.

Example:

```text
main
```

followed by:

```text
wipstream start
```

with branch name:

```text
histories-rewrite
```

produces a new checked-out `histories-rewrite` branch whose recorded parent is `main`.

## 7.2 Input

Prompt for a branch name unless supplied through a future supported noninteractive option.

The name must be a valid Git branch name.

It must not already exist:

- locally; or
- in the fetched remote state.

## 7.3 Behavior

The command:

1. Requires an ordinary checked-out parent branch.
2. Records its current commit.
3. Creates the new branch at that commit.
4. Switches to the new branch.
5. Records:

```text
branch.<new-branch>.wipstreamParent=<parent>
```

6. Records/completes the WipStream operation.

Existing uncommitted working-tree changes carry into the new branch as normal Git checkout behavior permits.

`start` does not itself commit or publish those changes.

---

# 8. `wipstream update`

## 8.1 Purpose

Bring the recorded parent branch's current history into the checked-out work branch.

This is used when the parent advanced independently after the work branch was created.

## 8.2 Initial retrieval

`update` first performs the equivalent safety/retrieval work of `wipstream get`.

The intended checked-out work branch must remain checked out after retrieval.

If retrieval causes the checkout to change, `update` stops and requires the user to select the intended branch and retry.

## 8.3 Parent resolution

Use the recorded WipStream parent when available.

For a branch imported from outside WipStream, parent selection follows the reference implementation, including confirmation rather than silently inventing durable ancestry metadata.

The parent must:

- exist locally;
- differ from the work branch;
- be a valid branch.

## 8.4 No-op case

If the parent is already an ancestor of the work branch, no merge is necessary.

Report that the branch is already current.

## 8.5 Merge behavior

Otherwise:

1. Record the work branch's pre-merge commit, index state, status, and exact parent commit to merge.
2. Create a recovery ref for the pre-merge branch tip.
3. Record a pending merge in the operation receipt.
4. Merge the exact resolved parent commit into the work branch.

If the merge succeeds:

- record the new branch tip;
- complete the operation.

If the merge conflicts:

- leave Git's merge state intact;
- update the pending-merge receipt with conflict paths;
- report that the merge is pending;
- require `wipstream continue` or `wipstream abort`.

`update` does not silently rebase.

A successful `update` does not by itself imply that the resulting merge commit has been published. Subsequent `save` or `finish` performs the remote handoff.

---

# 9. `wipstream finish`

## 9.1 Purpose

Incorporate a completed work branch into its parent and optionally remove the work branch.

This is a fast-forward-style branch completion operation, not a squash or arbitrary merge.

## 9.2 Preconditions

The checked-out branch:

- must be an ordinary work branch;
- must not be the remote default branch;
- must have or resolve to a valid parent.

The parent must already be an ancestor of the work branch.

If the parent advanced independently, refuse with guidance to run:

```text
wipstream update
```

first.

## 9.3 User decision

Before saving or publishing, show the resolved relationship and ask whether to:

- retain the completed work branch; or
- delete it after finishing.

Cancellation at this point must not save or publish work merely because `finish` was invoked.

## 9.4 Save prerequisite

After confirmation, `finish` performs `Commit and Save` semantics for the work branch state.

If the handoff does not complete successfully, `finish` must not advance the parent.

## 9.5 Finish transition

After successful handoff and fresh parity verification:

1. Reconfirm the selected branch and parent relationship.
2. Verify the parent remains an ancestor of the work branch.
3. Advance the parent branch to the work branch tip locally.
4. Advance the parent branch to the same tip remotely using exact expected remote state.
5. Switch the checkout to the parent.

If the user selected deletion:

6. Delete the work branch remotely.
7. Delete it locally.
8. Remove its branch-specific configuration.

If the user selected retention:

6. Retain the branch.
7. Ensure its WipStream parent metadata remains correct.

All remote changes that belong to this finish operation should preserve the existing atomic publication semantics.

## 9.6 Postcondition

The parent contains the completed work.

The repository finishes checked out on the parent.

Local and remote ordinary branch state must satisfy the reference implementation's parity requirements.

---

# 10. `wipstream reconcile`

## 10.1 Purpose

Explicitly reconcile the checked-out branch when its local and fetched remote histories have diverged.

WipStream resolves this condition with a merge, never a silent rebase.

## 10.2 Preconditions

The repository must:

- be initialized;
- have a clean working tree;
- have an ordinary checked-out branch;
- have no blocking active operation or incomplete operation.

After fetching, the checked-out branch must actually be classified as diverged.

If it is not diverged, direct the user back to ordinary `save` synchronization.

The command refuses to reconcile one branch while other ordinary branches also contain unresolved divergence requiring attention.

## 10.3 Merge behavior

The command:

1. Fetches remote state.
2. Identifies the exact fetched remote commit corresponding to the checked-out branch.
3. Records pre-merge HEAD, index state, status, and merge target.
4. Creates a recovery ref for the pre-merge tip.
5. Records a pending WipStream merge.
6. Merges the exact fetched remote branch commit into the local branch.

If the merge conflicts:

- leave the merge active;
- retain the operation receipt;
- record conflict paths;
- report the pending state.

If the merge succeeds:

- record the resulting branch transition;
- complete the merge operation;
- run Commit and Save to publish/synchronize the reconciled branch.

---

# 11. `wipstream continue`

## 11.1 Purpose

Complete exactly one pending WipStream merge after the user has resolved its conflicts.

This applies to pending merges produced by:

- `update`;
- `reconcile`.

## 11.2 Preconditions

Exactly one WipStream pending merge must exist.

All conflict paths must have been resolved.

The checked-out branch and Git merge state must still correspond to the recorded pending merge.

## 11.3 Behavior

If Git still has the recorded merge in progress:

1. Verify that it is the expected WipStream merge.
2. Stage all resolved files.
3. Create the merge commit using Git's merge state.
4. Record the resulting branch tip.
5. Complete the original operation receipt.
6. Run Commit and Save.

If the merge was already completed externally:

- recognize the completed merge conservatively where the stored evidence proves it;
- close/recover the original WipStream operation appropriately;
- proceed according to the reference semantics.

If Git already aborted the merge:

- close the stale WipStream state where safely provable;
- report that the user should synchronize or retry rather than fabricate a completed merge.

If Git no longer exposes enough state to prove what happened, direct the user to `recover`.

---

# 12. `wipstream abort`

## 12.1 Purpose

Abort exactly one pending WipStream merge and verify restoration of the recorded pre-merge state.

## 12.2 Preconditions

Exactly one WipStream pending merge must exist.

If Git still reports the merge:

1. Verify it is the recorded merge.
2. Run Git's merge-abort operation.
3. Verify restoration of:
   - branch;
   - HEAD;
   - working-tree status;
   - index tree;
   - absence of the merge operation.
4. Mark the WipStream operation aborted only after successful verification.

If the merge was already aborted externally, recognize and close it when the original state can be proven.

If the expected merge state cannot be established, do not manufacture restoration. Direct the user to `recover`.

---

# 13. `wipstream recover`

## 13.1 Purpose

Close an incomplete WipStream operation while preserving the repository's current state.

Recovery is deliberately conservative.

It does not replay the original operation and does not assert that remote synchronization succeeded.

## 13.2 Selection

If more than one recoverable incomplete operation exists, show the available operations and require the user to select one.

Display enough information to identify:

- operation ID;
- command;
- recorded phase/status;
- relevant branch/checkout information;
- retained backup path when applicable.

Require explicit confirmation to keep the current state.

## 13.3 Stale command lock

Recovery may reclaim a provably stale WipStream command lock according to the existing lock-recovery protocol.

It must not steal a lock from an active or unverifiable owner.

## 13.4 Preconditions

Before recording recovery:

- exactly one ordinary worktree must be in use;
- no Git operation may still be active;
- unresolved conflicts must not remain.

A pending WipStream merge must normally be handled through `continue` or `abort` while Git still knows about that merge.

## 13.5 Behavior

Recovery:

1. Preserves current files.
2. Preserves staged files.
3. Preserves untracked files.
4. Preserves current commits and refs.
5. Preserves existing recovery refs/history.
6. Does not rerun the failed plan.
7. Does not move ordinary refs merely to approximate the original intent.
8. Does not push.
9. Does not claim that a handoff completed.
10. Records the operation as recovered with resolution `kept-current-state`, including current branch and HEAD where available.

After recovery, the user may perform the appropriate normal WipStream command from the preserved state.

Interrupted remote adoption remains subject to its special backup/retry semantics.

---

# 14. `wipstream undo`

## 14.1 Purpose

Reverse the latest eligible completed WipStream action when repository state still exactly permits safe reversal.

Undo is not a general Git undo facility.

## 14.2 Currently undoable operation classes

The compatibility implementation recognizes these completed operations as candidates:

```text
Get from Remote
Initialize Repository
Commit and Save
Finish Branch
Condense Branch
Update from Parent
```

Remote adoption is explicitly not undoable through this mechanism.

Recovered operations are not made newly undoable merely because recovery closed them.

## 14.3 Eligibility

Undo is refused if:

- an incomplete operation exists;
- no eligible completed operation exists;
- a Git operation or conflict is active;
- the working tree changed after the operation;
- the checkout changed;
- relevant ordinary local refs changed;
- relevant WipStream configuration changed;
- the recorded remote-default state changed;
- fetched remote state no longer matches the operation's recorded completed state;
- any remote ref to be reversed has subsequently changed.

Undo relies on exact recorded state, not on a best-effort reconstruction.

## 14.4 Behavior

For an eligible operation:

1. Fetch current remote state.
2. Verify remote state still matches the completed operation.
3. Construct exact reverse remote transitions.
4. Construct exact reverse local ref transitions.
5. Reverse configuration changes.
6. Restore previous checkout.
7. Restore checkpoint working changes when the original operation created a checkpoint and its undo semantics require restoration.
8. Restore the remote-default tracking transition where applicable.
9. Use exact expected values and normal WipStream operation journaling for the undo itself.
10. Verify resulting configuration/state.
11. Complete the undo operation.
12. Mark the original operation as undone.

Undo itself is therefore another recorded WipStream operation.

---

# 15. `wipstream condense`

## 15.1 Purpose

Replace all commits exclusive to the checked-out work branch relative to its parent with one new commit having the same final tree.

This command is primarily useful for collapsing accumulated WIP checkpoint commits.

Example:

```text
main --- A
          \
           B --- C --- D --- E   feature
```

becomes:

```text
main --- A
          \
           X                     feature
```

where `X` has:

- parent `A`;
- the same final tree as the old `feature` tip;
- a new user-supplied commit message.

## 15.2 Preconditions

The repository must satisfy normal lifecycle safety requirements.

The command requires:

- an ordinary checked-out branch;
- a valid resolved parent;
- complete fetched local/remote parity before rewriting;
- the parent to be an ancestor of the work branch;
- at least two commits exclusive to the work branch.

If the parent advanced independently, direct the user to `update` first.

## 15.3 Preview and confirmation

Show at least:

- branch;
- parent;
- current branch tip;
- number of exclusive commits that will be replaced.

Require confirmation before rewriting history.

Then request the new condensed commit message.

A blank message is invalid.

## 15.4 Behavior

The command:

1. Uses the current branch tip's tree as the content of the new commit.
2. Uses the parent branch tip as the new commit's parent.
3. Creates the new commit with the requested message.
4. Plans replacement of the work branch tip locally.
5. Plans replacement of the same branch remotely.
6. Uses the currently fetched remote tip as the exact expected remote value.
7. Pushes the rewritten branch with the established exact-lease/atomic semantics.
8. Refetches.
9. Temporarily detaches if necessary.
10. Moves the local branch through the normal ref-transaction/recovery mechanism.
11. Restores the branch checkout.
12. Verifies complete branch parity.
13. Completes the operation receipt.

The old commits remain recoverable according to WipStream's recovery-ref and Git object-retention behavior, but they are no longer part of the ordinary branch history.

---

# 16. Parent Resolution

Several lifecycle commands depend on a work branch's parent:

- `update`;
- `finish`;
- `condense`.

Branches created with `wipstream start` have explicit parent metadata:

```text
branch.<name>.wipstreamParent
```

For imported branches without recorded parent metadata, preserve the reference implementation's conservative parent-selection behavior.

Do not silently record a guessed parent merely because the remote default branch appears plausible.

Once the user explicitly confirms a parent in a context where the reference implementation persists it, write the same Git configuration key.

---

# 17. Branch Relationship Semantics

Commands that synchronize all branches classify local and fetched remote histories using the established WipStream relations:

| Relation | Meaning |
| --- | --- |
| `equal` | Local and fetched remote tips match |
| `local-ahead` | Local history strictly contains fetched remote history |
| `local-only` | Local branch has no current/previous remote counterpart |
| `remote-ahead` | Remote history strictly contains local history |
| `remote-only` | Remote branch has no local branch |
| `diverged` | Neither history contains the other |
| `remotely-deleted` | Branch existed in previously observed remote state but no longer exists remotely |

Commands must preserve the existing interpretation of these relationships.

In particular:

- `save` may publish `local-ahead` and `local-only`;
- `get` must refuse them as unpublished local work;
- both refuse divergence rather than guessing;
- safe remote deletion requires evidence that the local branch has not changed since the last observed remote tip.

---

# 18. Verification Principle

Each command must verify its intended postcondition.

Examples:

- `save`: completed remote handoff and expected branch parity;
- `get`: safe local adoption of fetched state;
- `finish`: parent now points to completed work and checkout is parent;
- `condense`: rewritten branch has the requested single exclusive commit and local/remote parity;
- `abort`: exact pre-merge state restored;
- `undo`: exact recorded operation reversed.

The implementation must not equate:

```text
git command exited successfully
```

with:

```text
WipStream operation succeeded
```

without the relevant final verification.

---

# 19. Compatibility Requirement

For every command described here, tests must include cases where repository state was produced by the other implementation.

Examples:

```text
TypeScript save
Go get
```

```text
Go save
TypeScript get
```

```text
TypeScript update -> conflict
Go continue
```

```text
Go reconcile -> conflict
TypeScript abort
```

```text
TypeScript condense
Go undo
```

```text
Go finish
TypeScript get
```

These interoperability tests use only disposable fixture repositories.

A repository state validly produced by one WipStream implementation but unusable by the other is a compatibility defect.