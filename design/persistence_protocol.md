# WipStream Persistent Metadata and Protocol Specification

## 1. Purpose

This document defines the persistent repository-local protocol shared by:

- the existing TypeScript WipStream VS Code extension; and
- the standalone Go implementation.

The Go implementation must be able to use a clone previously operated by the TypeScript implementation, and the TypeScript implementation must be able to use that same clone after Go has operated on it.

No migration step is permitted or expected.

The current TypeScript implementation at:

```text
https://github.com/lewisl/wip-stream
```

is the reference implementation for protocol details not fully specified here.

Relevant source files currently include:

```text
src/constants.ts
src/repository-model.ts
src/operations.ts
src/repository-safety.ts
src/generalized-workflow.ts
src/lifecycle-workflow.ts
src/conflict-workflow.ts
src/recovery-workflow.ts
src/undo-workflow.ts
```

This document describes persistent compatibility, not internal Go package design.

---

# 2. Protocol Scope

WipStream stores persistent state in four places associated with the managed Git clone:

```text
1. ordinary Git configuration
2. WipStream-private files in Git's common directory
3. WipStream-private Git refs
4. ordinary Git refs/configuration that WipStream maintains
```

For a conventional clone:

```text
project/
    .git/
    ...
```

some paths happen to appear beneath `.git`.

However, implementations MUST determine Git's actual common directory using Git:

```text
git rev-parse --git-common-dir
```

and must not assume that the common directory is literally `<project>/.git`.

WipStream requires the supported ordinary-clone layout described by the behavioral specifications, but path handling must still use Git's reported locations.

---

# 3. Clone Locality

WipStream operational metadata is local to a clone.

For example:

```text
Mac A
project/.git/wipstream/...

Mac B
project/.git/wipstream/...
```

contain independent operation histories.

This metadata is not pushed to the Git remote.

Cross-machine synchronization occurs through ordinary Git branches, commits, remote refs, and the selected remote.

The persistent protocol defined here matters primarily when the TypeScript and Go implementations are alternated against the same clone.

---

# 4. Compatibility Rule

Existing persistent metadata is a protocol, not an implementation detail that may be redesigned during the Go port.

The Go implementation MUST:

- read metadata written by TypeScript;
- write metadata TypeScript can read;
- preserve established field meanings;
- preserve established path and ref naming;
- preserve schema-version behavior;
- preserve mutation-boundary semantics;
- preserve lock interoperability;
- preserve recovery and undo evidence.

The Go implementation MAY use different in-memory structures provided serialization and semantics remain compatible.

When adding a future protocol field or schema version, compatibility with both implementations must be considered explicitly.

---

# 5. Repository Initialization Configuration

## 5.1 Selected remote

The clone's initialized/uninitialized state is represented by local Git configuration:

```text
wipstream.remote
```

Example:

```ini
[wipstream]
    remote = origin
```

Absence of this value means the repository is uninitialized from WipStream's perspective.

A present value means the repository is initialized for that selected remote.

The value must be a non-empty Git remote name.

The Go implementation MUST use the same key.

It must not create a parallel Go-specific initialization marker.

---

# 6. Branch Parent Metadata

WipStream records the intended parent of a work branch using local Git configuration:

```text
branch.<branch-name>.wipstreamParent
```

Example:

```ini
[branch "histories-rewrite"]
    wipstreamParent = feature
```

This metadata is used by operations including:

```text
Update from Parent
Finish Branch
Condense Branch
```

A valid parent:

- is a valid Git branch name;
- differs from the child branch.

Branches created by `Start Branch` receive this metadata.

Imported branches may receive it after explicit parent resolution according to command behavior.

The Go implementation MUST read and write this exact key.

---

# 7. Ordinary Git Configuration Maintained by WipStream

In addition to WipStream-private configuration, initialization and retrieval may maintain standard Git configuration.

These include:

```text
remote.<remote>.fetch
branch.<branch>.remote
branch.<branch>.merge
```

The initialized fetch mapping is normally:

```text
+refs/heads/*:refs/remotes/<remote>/*
```

Tracking configuration normally maps a local branch to the same-named branch on the selected remote:

```text
branch.<branch>.remote = <remote>
branch.<branch>.merge  = refs/heads/<branch>
```

These are normal Git settings rather than WipStream-private protocol fields, but operation plans may record and reverse their changes.

The Go implementation must preserve the same resulting Git behavior.

---

# 8. Remote Default Branch Cache

WipStream uses the ordinary symbolic remote-tracking ref:

```text
refs/remotes/<remote>/HEAD
```

to represent the locally cached remote default branch.

Example:

```text
refs/remotes/origin/HEAD
    -> refs/remotes/origin/main
```

Operation plans may record a `remoteHead` transition so that this state can be verified and, where appropriate, undone.

The server's actual default branch must still be obtained through Git/remote inspection when required; this symbolic ref is persistent local state, not unquestioned authority.

---

# 9. WipStream Common Directory

Private WipStream files live beneath:

```text
<git-common-dir>/wipstream/
```

Current persistent paths include:

```text
<git-common-dir>/wipstream/
    operations/
        <operation-id>.json

    command.lock
    command.lock.recovered-<operation-id>   # possible stale-lock archive
```

Temporary receipt files may exist transiently while a receipt is being written.

The Go implementation MUST use the same locations.

It must not introduce:

```text
.wipstream/
.wipstream-go/
.git/wipstream-go/
```

or another parallel store.

---

# 10. Operation Identifiers

Operation IDs must satisfy:

```text
^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$
```

The normal implementation generates UUIDs.

An operation ID is used in several related places:

```text
operation receipt filename
recovery ref namespace
lock identity
diagnostics
undo/recovery selection
```

Examples:

```text
<git-common-dir>/wipstream/operations/
    123e4567-e89b-12d3-a456-426614174000.json
```

and:

```text
refs/wipstream/recovery/
    123e4567-e89b-12d3-a456-426614174000/
        0000
```

The Go implementation should normally generate UUIDs compatible with this permitted syntax.

---

# 11. Internal WipStream Ref Namespace

All WipStream-private Git refs use:

```text
refs/wipstream/
```

as their namespace.

They are not ordinary branches and MUST be excluded from ordinary branch inventory.

Current uses include:

```text
refs/wipstream/recovery/<operation-id>/<ordinal>

refs/wipstream/command-lock-recovery
```

Future internal refs should remain within the same private namespace unless the protocol is deliberately revised.

---

# 12. Recovery Refs

## 12.1 Naming

Before replacing or deleting a local ref whose current value may be needed for recovery, WipStream creates a recovery ref:

```text
refs/wipstream/recovery/<operation-id>/<ordinal>
```

The ordinal is zero-based and rendered with at least four decimal digits:

```text
0000
0001
0002
...
```

Example:

```text
refs/wipstream/recovery/abc123/0000
```

## 12.2 Meaning

For each planned local ref update whose previous value is non-null and differs from the proposed value, the recovery ref points to that previous object ID.

Conceptually:

```text
planned local update:

refs/heads/feature
    old = AAA
    new = BBB
```

causes:

```text
refs/wipstream/recovery/<operation>/0000
    -> AAA
```

before or atomically with the local ref transition.

These refs keep displaced commits reachable and provide evidence for recovery/undo.

## 12.3 Transaction

Recovery-ref creation and associated ordinary local ref updates should occur through the same expected-old Git ref transaction wherever the reference implementation does so.

Recovery refs must not silently overwrite an existing ref.

---

# 13. Local Ref Update Record

Persistent operation plans describe local ref transitions using:

```text
ref
expectedOld
proposed
```

Conceptual shape:

```json
{
  "ref": "refs/heads/feature",
  "expectedOld": "aaaaaaaa...",
  "proposed": "bbbbbbbb..."
}
```

Values mean:

```text
expectedOld = null
    ref must not exist

proposed = null
    delete ref

expectedOld != null
proposed != null
    replace exact expected object with proposed object
```

Both may not be null simultaneously.

The ref must be a full Git ref name.

Duplicate updates for the same ref within the same update list are invalid.

---

# 14. Remote Ref Update Record

Current schema-2 operation plans represent each remote transition as one record:

```text
ref
expected
proposed
```

Conceptual shape:

```json
{
  "ref": "refs/heads/feature",
  "expected": "aaaaaaaa...",
  "proposed": "bbbbbbbb..."
}
```

Semantics:

```text
expected = null
    remote ref must not exist

proposed = null
    delete remote ref

expected != null
proposed != null
    replace exact expected remote value
```

The `expected` field represents the remote lease.

Remote publication must preserve exact-expected-value semantics and atomic publication where required.

The Go implementation must not transform these records into unconditional force pushes.

---

# 15. Operation Plan

## 15.1 Current writer schema

New operation plans MUST be written as:

```text
schemaVersion = 2
```

Current conceptual structure:

```json
{
  "schemaVersion": 2,
  "operationId": "...",
  "command": "...",
  "createdAt": "...",

  "localRefUpdates": [],
  "remoteRefUpdates": [],

  "checkpoint": {},
  "checkpointRestoration": {},
  "remoteHead": {},
  "remoteAdoption": {},

  "configurationChanges": [],
  "checkout": {},
  "destructiveEffects": []
}
```

Optional fields are omitted when not applicable.

Arrays that are part of the base plan remain present according to the established schema.

## 15.2 Required base fields

A schema-2 plan contains:

```text
schemaVersion
operationId
command
createdAt
localRefUpdates
remoteRefUpdates
configurationChanges
checkout
destructiveEffects
```

`command` must be non-empty.

`createdAt` is an ISO-style timestamp as written by the reference implementation.

The Go implementation should use RFC 3339 / UTC formatting compatible with JavaScript `Date.toISOString()`.

---

# 16. Checkout Transition

Plan field:

```text
checkout
```

shape:

```json
{
  "before": "feature",
  "after": "main"
}
```

Either value may be absent where no named branch exists.

The field records intended checkout state, not merely branch-ref changes.

Undo and postcondition verification may depend on it.

---

# 17. Checkpoint Transition

Optional plan field:

```text
checkpoint
```

shape:

```json
{
  "branch": "feature",
  "before": "aaaaaaaa...",
  "after": "bbbbbbbb...",
  "message": "WIP checkpoint ..."
}
```

It records a checkpoint commit created before the larger synchronization operation completes.

This distinction is important because a checkpoint may remain locally even when later publication fails.

Undo may use this information to restore the checkpoint's working changes.

---

# 18. Checkpoint Restoration

An undo operation may include:

```text
checkpointRestoration
```

shape:

```json
{
  "before": "aaaaaaaa...",
  "after": "bbbbbbbb..."
}
```

This records the commit transition whose file changes are to be restored to the working state during undo.

An interrupted checkpoint restoration must remain recoverable through normal receipt/recovery semantics.

---

# 19. Configuration Transition

Configuration changes are represented as:

```json
{
  "key": "branch.feature.remote",
  "before": ["old-value"],
  "after": ["origin"]
}
```

Both `before` and `after` are arrays.

This permits representation of:

- absent keys;
- one value;
- multiple values.

Examples:

```text
before = []
after  = ["origin"]
```

means create/configure the key.

```text
before = ["origin"]
after  = []
```

means remove it.

Undo reverses these transitions.

---

# 20. Remote Head Transition

Optional field:

```text
remoteHead
```

shape:

```json
{
  "remote": "origin",
  "before": "refs/remotes/origin/main",
  "after": "refs/remotes/origin/trunk"
}
```

`before` and `after` may be `null`.

It records changes to:

```text
refs/remotes/<remote>/HEAD
```

and participates in postcondition verification and Undo.

---

# 21. Destructive Effects

A plan contains:

```text
destructiveEffects
```

Each item contains:

```text
kind
optional ref
description
```

Current kinds are:

```text
delete-local-ref
delete-remote-ref
rewrite-local-ref
rewrite-remote-ref
replace-files
replace-checkout
```

These records describe effects for inspection/recovery/preview purposes.

They are not a substitute for the exact transition records.

---

# 22. Remote Adoption Metadata

Remote-authority initialization may store:

```text
remoteAdoption
```

with the following conceptual fields:

```json
{
  "remote": "origin",
  "remoteDefaultBranch": "main",

  "fetchedTips": [
    {
      "name": "main",
      "tip": "aaaaaaaa..."
    }
  ],

  "worktreeFingerprint": "...",

  "trackedPaths": [],
  "removedUntrackedPaths": [],
  "preservedIgnoredPaths": [],

  "backup": {
    "kind": "verified-copy",
    "path": "/..."
  }
}
```

or:

```json
{
  "backup": {
    "kind": "explicit-discard"
  }
}
```

The metadata records the authority decision and preservation/replacement scope.

The Go implementation must preserve this representation because:

- recovery may display the retained backup path;
- Undo must recognize that remote adoption is not normally undoable;
- compatibility tests may hand interrupted adoption state between implementations.

---

# 23. Legacy Operation Plan Schema 1

The existing TypeScript reader supports both:

```text
plan schemaVersion 1
plan schemaVersion 2
```

The Go implementation MUST therefore read both.

The Go implementation SHOULD write only schema version 2.

## 23.1 Schema-1 remote representation

Legacy schema-1 plans separated proposed remote changes from leases.

They contained:

```text
remoteRefUpdates
remoteLeases
```

Conceptually:

```json
{
  "remoteRefUpdates": [
    {
      "ref": "refs/heads/feature",
      "proposed": "bbbbbbbb..."
    }
  ],

  "remoteLeases": [
    {
      "ref": "refs/heads/feature",
      "expected": "aaaaaaaa..."
    }
  ]
}
```

When reading schema 1:

- each remote update must have exactly one matching lease;
- duplicate refs are invalid;
- unmatched updates or leases are invalid.

The reader normalizes these into the schema-2 conceptual form:

```json
{
  "ref": "refs/heads/feature",
  "expected": "aaaaaaaa...",
  "proposed": "bbbbbbbb..."
}
```

Go should perform the same compatibility normalization internally.

It should not rewrite an old receipt merely because it read it.

---

# 24. Operation Receipt Location

Receipts are stored at:

```text
<git-common-dir>/wipstream/operations/<operation-id>.json
```

The filename operation ID must equal:

```text
receipt.plan.operationId
```

A mismatch is invalid.

Receipt files are local operational records and are not committed or pushed.

---

# 25. Operation Receipt Schema

The current receipt schema is:

```text
schemaVersion = 1
```

This is independent of the embedded plan schema.

Therefore a normal newly written receipt currently contains:

```text
receipt schemaVersion = 1
plan schemaVersion    = 2
```

Conceptual structure:

```json
{
  "schemaVersion": 1,

  "plan": {},

  "phase": "planned",
  "status": "planned",

  "events": [
    {
      "phase": "planned",
      "recordedAt": "..."
    }
  ],

  "pendingMerge": {},
  "outcome": {},
  "completedAt": "...",
  "recovery": {}
}
```

Optional sections are omitted when not applicable.

The Go implementation MUST preserve this schema numbering.

It must not assume that receipt and plan schema versions advance together.

---

# 26. Operation Status

Current status values are:

```text
planned
in-progress
completed
aborted
undone
recovered
```

A receipt is considered incomplete when status is:

```text
planned
in-progress
```

Normal workflows inspect incomplete receipts and refuse conflicting work until the incomplete operation is resolved or recovered.

---

# 27. Mutation Boundaries and Phases

Current mutation boundaries are:

```text
local-refs
remote-push
remote-fetch
checkout
configuration
file-replacement
remote-head
checkpoint-restoration
merge
```

Each boundary is journaled using:

```text
before-<boundary>
after-<boundary>
```

Examples:

```text
before-remote-push
after-remote-push

before-local-refs
after-local-refs

before-merge
after-merge
```

Terminal phases are:

```text
completed
aborted
undone
recovered
```

Initial phase:

```text
planned
```

---

# 28. Phase Transition Rules

The journal follows these rules.

From:

```text
planned
```

an operation may enter:

```text
before-<boundary>
```

or complete directly where appropriate.

After:

```text
after-<boundary>
```

it may enter another:

```text
before-<boundary>
```

or complete.

A matching after-phase may follow only its exact before-phase:

```text
before-remote-push
    ->
after-remote-push
```

not:

```text
before-remote-push
    ->
after-local-refs
```

Entering a mutation boundary changes the operation from merely planned to in progress.

If execution stops after a `before-*` phase but before its matching `after-*`, the receipt deliberately records uncertainty about whether that mutation completed.

Recovery logic must preserve that uncertainty rather than assuming failure or success.

---

# 29. Receipt Events

Every phase transition is appended to:

```text
events
```

Each event contains:

```text
phase
recordedAt
```

Example:

```json
{
  "phase": "before-remote-push",
  "recordedAt": "2026-10-05T20:00:00.000Z"
}
```

The event history is append-only for normal receipt progression.

Current phase reflects the latest recorded state.

---

# 30. Starting an Operation

A newly recorded operation receipt begins:

```text
phase  = planned
status = planned
```

with exactly an initial `planned` event.

Receipt creation must be exclusive.

An existing receipt with the same operation ID must not be overwritten.

If a command refuses or fails before entering any mutation boundary, its own untouched planned receipt may be closed as aborted.

Once a mutation boundary has been entered, an interrupted operation requires recovery semantics.

---

# 31. Receipt Write Safety

Receipt updates are critical recovery data.

Equivalent Go behavior must provide the same essential guarantees:

### New receipt

Creation must be exclusive.

A new operation must not overwrite an existing receipt with the same ID.

### Receipt update

Updates should be written through an atomic replacement strategy in the same directory.

### Durability

The new receipt content should be flushed before it becomes the authoritative file.

### Permissions

The reference implementation creates receipt temporary files with mode:

```text
0600
```

where the platform supports POSIX permissions.

Exact Node filesystem calls need not be reproduced, but the safety properties must be preserved.

---

# 32. Pending Merge

A receipt may contain:

```text
pendingMerge
```

shape:

```json
{
  "kind": "merge",
  "command": "Update from Parent",
  "branch": "feature",
  "mergeTarget": "refs/heads/main",
  "mergeTargetCommit": "aaaaaaaa...",
  "preHead": "bbbbbbbb...",
  "preIndexTree": "cccccccc...",
  "preStatus": "...",
  "conflicts": []
}
```

`command` is currently one of:

```text
Update from Parent
Reconcile with Remote
```

`mergeTargetCommit` is optional for compatibility with older receipts.

New Go-created pending merges SHOULD include it.

The exact commit ID is preferred because it lets recovery distinguish the recorded merge from later movement of the named ref.

The remaining fields preserve enough pre-merge state to verify completion or abort.

---

# 33. Pending Merge Recording Point

A pending merge record is written while the receipt is at:

```text
phase = before-merge
status = in-progress
```

If a merge conflicts, the receipt remains incomplete with Git's merge state preserved.

Conflict paths may be added to the pending merge record.

Either implementation must then be able to run:

```text
continue
```

or:

```text
abort
```

against the state created by the other implementation.

---

# 34. Operation Outcome

Receipts may contain:

```text
outcome
```

Current fields include:

```text
additionalLocalRefUpdates
completedLocalRefs
completedRemoteRefs
completedCheckout
completedStatus
```

## 34.1 Additional local ref updates

Some local transitions arise during execution rather than appearing in the original static plan.

These are recorded as:

```text
additionalLocalRefUpdates
```

using the normal local ref update shape.

## 34.2 Completed local refs

At successful completion, WipStream records the ordinary local branch refs:

```text
completedLocalRefs
```

entries:

```json
{
  "ref": "refs/heads/feature",
  "objectId": "..."
}
```

## 34.3 Completed remote refs

At successful completion, WipStream records fetched remote-tracking refs, excluding symbolic `/HEAD` entries:

```text
completedRemoteRefs
```

## 34.4 Completed checkout

The named branch checked out at completion may be stored as:

```text
completedCheckout
```

## 34.5 Completed status

The final Git porcelain status may be stored as:

```text
completedStatus
```

These snapshots are significant to Undo eligibility and subsequent verification.

---

# 35. Operation Completion

Before marking an operation completed, the implementation records final outcome snapshots.

Then the receipt transitions to:

```text
phase  = completed
status = completed
```

and records:

```text
completedAt
```

The completion event is appended to `events`.

A completed operation is not automatically undoable; Undo eligibility performs additional checks.

---

# 36. Aborted Operations

A started operation may become:

```text
phase  = aborted
status = aborted
```

with:

```text
completedAt
```

and an appended event.

A command that created a receipt but never entered a mutation boundary may also close its untouched `planned` receipt as aborted.

This distinguishes:

```text
nothing mutated
```

from:

```text
an operation entered a mutation boundary and its outcome may require inspection
```

---

# 37. Recovery Record

An incomplete operation may be explicitly closed with:

```text
phase  = recovered
status = recovered
```

and a `recovery` object.

Current recovery resolutions are:

```text
merge-completed-externally
merge-aborted-externally
kept-current-state
```

Possible recovery fields include:

```text
branch
head
mergeCommit
```

Example:

```json
{
  "resolution": "kept-current-state",
  "branch": "feature",
  "head": "aaaaaaaa..."
}
```

Explicit recovery does not make the recovered operation eligible for ordinary Undo.

---

# 38. Undone Operations

After an eligible completed operation has itself been reversed through a recorded Undo operation, the original receipt becomes:

```text
phase  = undone
status = undone
```

and receives an appended `undone` event.

The Undo itself has its own independent operation ID and receipt.

Thus history contains both:

```text
original action
undo action
```

rather than erasing the evidence of the original operation.

---

# 39. Receipt Retention

Current behavior retains up to:

```text
50
```

receipts whose current status is:

```text
completed
```

when completion pruning runs.

Older completed receipts beyond that retention count may be removed.

The reference implementation currently prunes the `completed` category specifically rather than treating every terminal status identically.

The Go implementation should preserve this behavior initially.

A future retention-policy redesign should be made deliberately in both implementations rather than as part of the port.

---

# 40. Command Lock Location

The active command lock is:

```text
<git-common-dir>/wipstream/command.lock
```

Only one WipStream mutation should own this lock in a clone at one time.

Both implementations MUST recognize locks created by the other.

---

# 41. Command Lock Schema

Current lock schema:

```text
schemaVersion = 1
```

Conceptual shape:

```json
{
  "schemaVersion": 1,
  "operationId": "...",
  "command": "Commit and Save",
  "pid": 12345,
  "hostname": "machine-name",
  "startedAt": "2026-10-05T20:00:00.000Z",
  "repositoryRoot": "/path/to/project"
}
```

Fields:

### `operationId`

Unique identity for this lock ownership instance.

It need not equal a later workflow receipt operation ID.

### `command`

Human-readable WipStream command owning the lock.

### `pid`

Positive local process ID.

### `hostname`

Host identity used to determine whether process-liveness tests are meaningful.

### `startedAt`

Creation timestamp.

### `repositoryRoot`

Canonical repository root associated with the lock.

---

# 42. Lock Creation

Lock creation must be exclusive.

A second process must not overwrite an existing lock.

The reference implementation creates:

```text
command.lock
```

with exclusive-create semantics and mode:

```text
0600
```

where applicable.

If a lock already exists:

- an active verified owner blocks the new command;
- a dead same-host owner is considered stale but is not silently deleted;
- unreadable or unverifiable lock state is preserved for inspection.

The user is directed through the recovery workflow rather than automatic destructive lock cleanup.

---

# 43. Lock Release

A process may remove the lock only if the current lock still identifies that same ownership record.

If the lock changed while the command was running, it must be left for inspection.

One implementation must never delete a lock merely because it has the expected pathname.

Ownership identity must be verified.

---

# 44. Stale Lock Recovery Lease

Stale-lock recovery is serialized through:

```text
refs/wipstream/command-lock-recovery
```

This ref points to a Git blob containing a JSON `CommandLockRecord` identifying the recovery owner.

This provides an exact-old Git ref lease so competing recovery attempts cannot both believe they own stale-lock recovery.

The Go implementation MUST participate in the same lease protocol.

It must not bypass the ref simply because its own filesystem locking mechanism differs.

---

# 45. Lock Recovery Owner Blob

The blob stored under the lock-recovery ref contains the same schema-1 lock record shape:

```json
{
  "schemaVersion": 1,
  "operationId": "...",
  "command": "Recover command lock",
  "pid": 12345,
  "hostname": "...",
  "startedAt": "...",
  "repositoryRoot": "..."
}
```

Recovery may proceed only when any previous lease owner is provably stale according to the established host/process rules.

An active, cross-host, malformed, or otherwise unverifiable lease must be preserved.

---

# 46. Stale Lock Archive

After safely proving a filesystem command lock stale, the reference implementation renames it to:

```text
command.lock.recovered-<recovery-operation-id>
```

rather than deleting the evidence.

The Go implementation should preserve this convention.

These archived files are not active locks.

---

# 47. Process-Liveness Compatibility

A same-host lock may be considered stale only when its recorded process can be established as no longer alive.

A lock from another hostname must not be declared stale merely because its PID is absent locally.

The Go implementation should provide platform-appropriate process-liveness checks while preserving this semantic rule.

On platforms supporting user ownership checks, recovery should also preserve equivalent protection against reclaiming another user's lock file.

---

# 48. Filesystem Lock vs Operation Receipt

The command lock and operation receipts serve different purposes.

```text
command.lock
    prevents concurrent WipStream commands

operation receipt
    records durable mutation intent and progress
```

The lock normally disappears after command completion.

Receipts persist.

A crash may leave both.

Recovery of one must not casually discard the other.

---

# 49. Internal Refs and Ordinary Branch Inventory

The internal prefix:

```text
refs/wipstream/
```

must be excluded from:

- ordinary local branch enumeration;
- synchronization branch lists;
- remote handoff calculations;
- parent lifecycle logic.

Ordinary branches are represented by:

```text
refs/heads/*
```

and selected remote observations by:

```text
refs/remotes/<remote>/*
```

WipStream's private refs are recovery infrastructure, not user work branches.

---

# 50. Remote-Tracking Snapshots

Several workflows depend on the distinction between:

```text
previously observed remote tip
```

and:

```text
newly fetched remote tip
```

This is necessary to distinguish, among other things:

```text
local-only branch
```

from:

```text
branch that existed remotely and was then deleted
```

The pre-fetch remote-tracking state is ordinary Git metadata rather than a separate WipStream file.

The Go implementation must preserve the same observation ordering:

```text
snapshot remote-tracking refs
fetch/prune
inspect fetched state
classify relationship
```

Changing that order would change protocol semantics even though no JSON schema changed.

---

# 51. Branch Relationship Vocabulary

Persistent receipts and diagnostics rely on the same conceptual branch model used by the TypeScript implementation.

Relations are:

```text
equal
local-ahead
local-only
remote-ahead
remote-only
diverged
remotely-deleted
```

Remote-tip change classifications include:

```text
unseen
created
unchanged
advanced
rewritten
deleted
```

Tracking classifications include:

```text
none
selected-remote
other-remote
```

These classifications are primarily runtime observations rather than separately persisted protocol objects, but their semantics determine what transitions are written into plans and receipts.

They therefore form part of the behavioral compatibility contract.

---

# 52. Timestamps

Persisted timestamps should be written in a format compatible with JavaScript:

```text
new Date().toISOString()
```

Example:

```text
2026-10-05T20:30:45.123Z
```

Go should use UTC RFC 3339 formatting with compatible fractional-second precision.

Readers should not depend on timestamps being generated by Node.

Timestamps are evidence and ordering data; Git object IDs and exact state transitions remain the authoritative identities for repository mutation.

---

# 53. JSON Compatibility

Go JSON serialization must preserve established property names exactly.

For example:

```text
schemaVersion
operationId
createdAt
localRefUpdates
remoteRefUpdates
expectedOld
configurationChanges
pendingMerge
completedAt
```

must not become Go-style exported field names such as:

```text
SchemaVersion
OperationID
CreatedAt
```

in JSON.

Go structs therefore require explicit JSON tags.

Optional fields should follow the current omission semantics.

The implementation should not introduce `null` fields where the established writer omits them unless the existing reader explicitly treats them equivalently.

---

# 54. Unknown and Legacy Data

The Go reader should be conservative.

It must:

- accept known legacy plan schema 1;
- accept current plan schema 2;
- accept current receipt schema 1;
- reject malformed operation IDs;
- reject internally inconsistent ref updates;
- reject unsupported future schema versions rather than guessing their meaning.

It should not silently repair malformed persistent metadata.

If metadata is unreadable or inconsistent, preserve it and report the problem.

---

# 55. Forward Compatibility Principle

Neither implementation should assume it can safely interpret an unknown schema version.

Future protocol evolution should use explicit schema-version changes when field meanings or required invariants change.

A future reader may support multiple versions by normalizing them internally, as the current TypeScript implementation already does for operation-plan schemas 1 and 2.

Writers should normally emit only the newest mutually supported format.

---

# 56. No Go-Specific Persistent State

The initial Go port MUST NOT add required persistent state such as:

```text
wipstream.goVersion
wipstreamGo.remote
.git/wipstream/go-state.json
refs/wipstream-go/*
```

Merely identifying which implementation last operated on a repository is not needed.

Both implementations are clients of the same WipStream protocol.

If diagnostic implementation/version metadata is ever desired, it must be optional and harmless to older clients.

---

# 57. Cross-Implementation Protocol Tests

Protocol tests must explicitly create state in one implementation and consume it in the other.

At minimum:

## 57.1 Configuration

```text
TS initialize
Go reads initialization + parents

Go initialize
TS reads initialization + parents
```

## 57.2 Completed receipts

```text
TS completes operation
Go reads receipt and evaluates Undo

Go completes operation
TS reads receipt and evaluates Undo
```

## 57.3 Pending merge

```text
TS creates conflicted pending merge
Go continue/abort
```

and:

```text
Go creates conflicted pending merge
TS continue/abort
```

## 57.4 Recovery refs

Each implementation must recognize and preserve recovery refs created by the other.

## 57.5 Incomplete operation

```text
TS interruption
Go recover
TS continues normal work
```

and the reverse.

## 57.6 Undo

An operation created by one implementation must be undoable by the other when all ordinary eligibility conditions remain true.

## 57.7 Command locks

Testing should include compatible parsing of:

- active lock records;
- stale lock records;
- stale lock archive behavior;
- lock-recovery leases.

Actual concurrent-process tests should be isolated to disposable fixtures.

---

# 58. Byte Identity vs Semantic Identity

Interoperability does not require byte-for-byte identical JSON formatting.

For example, Go need not reproduce exactly:

```text
two-space indentation
```

provided both implementations can read the result.

However, the Go implementation SHOULD use stable, human-readable JSON and a trailing newline, matching the existing style where practical.

What must be identical is:

- field names;
- schema values;
- field meanings;
- null/absence semantics where significant;
- operation/ref identities;
- transition values;
- phase/status semantics.

---

# 59. Source of Truth During Porting

When this document does not answer a protocol question:

1. inspect the current TypeScript implementation;
2. inspect its tests;
3. determine the actual established behavior;
4. add or clarify the protocol specification if the behavior is important;
5. implement the Go behavior compatibly.

Do not guess.

Do not redesign the protocol simply because a different representation would be more idiomatic in Go.

---

# 60. Initial Protocol Success Criteria

The persistent protocol portion of the Go port is complete when:

1. Go reads existing initialized clone configuration.
2. TypeScript reads clone configuration written by Go.
3. Branch-parent metadata is interchangeable.
4. Go reads schema-1 and schema-2 operation plans.
5. Go writes schema-2 plans.
6. Both implementations read receipt schema 1 written by the other.
7. Recovery refs use exactly the shared namespace and naming convention.
8. Pending merges can cross implementation boundaries.
9. Explicit recovery can cross implementation boundaries.
10. Undo can operate on eligible actions created by the other implementation.
11. Command locks and stale-lock recovery are interoperable.
12. Neither implementation requires implementation-specific repository migration.
13. All protocol tests use disposable repositories.
14. No Go-specific required persistent state has been introduced.