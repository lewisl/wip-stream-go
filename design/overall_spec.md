# WipStream Go Port Specification

## 1. Purpose

`wip-stream-go` is a standalone Go implementation of the existing WipStream workflow.

The resulting executable will be named:

```text
wipstream
```

The Go implementation is intended to coexist with the existing WipStream VS Code extension. It is not a replacement protocol, a redesign of the workflow, or a general-purpose Git client.

A user must be able to operate a repository with the VS Code extension, then use the Go CLI on the same clone, then return to the VS Code extension without either implementation requiring migration, repair, or special coordination.

Compatibility failures between the two implementations are bugs.

## 2. Primary Goals

The Go implementation MUST:

1. Provide the same public WipStream operations exposed by the current VS Code extension.
2. Preserve the behavioral semantics of the TypeScript implementation.
3. Use the same persistent WipStream metadata stored in each managed repository's Git metadata.
4. Read metadata written by the TypeScript implementation.
5. Write metadata that the TypeScript implementation can subsequently read and use.
6. Preserve the existing safety, refusal, recovery, and undo semantics.
7. Use the installed Git executable for all Git operations.
8. Produce a standalone executable requiring neither Node.js nor VS Code at runtime.
9. Use idiomatic, understandable Go rather than mechanically translating TypeScript structures.
10. Be suitable as a relatively small Go codebase for learning and long-term maintenance.

Runtime speed, executable size, and memory consumption are not significant design concerns for this project.

Correctness, comprehensibility, interoperability, and safety take priority.

## 3. Reference Implementation

The current TypeScript WipStream implementation is the behavioral and compatibility reference.

It defines:

- workflow semantics;
- safety rules;
- command behavior;
- persistent metadata formats;
- Git configuration keys;
- private Git ref conventions;
- operation receipt schemas;
- recovery semantics;
- undo behavior;
- branch-parent metadata;
- repository initialization behavior;
- remote synchronization behavior.

The TypeScript source defines required behavior, but it does NOT define the required internal architecture of the Go implementation.

The Go implementation SHOULD use normal Go designs where they improve clarity.

A difference between the implementations MUST initially be treated as a Go-port defect unless investigation establishes that:

1. the TypeScript implementation itself contains a bug; or
2. the behavior is deliberately changed in both implementations as a separately reviewed protocol change.

The initial port MUST NOT silently "improve," simplify, or redesign persistent behavior.

## 4. Product Boundary

WipStream is a workflow coordinator built on Git.

It is NOT:

- a replacement Git implementation;
- a general Git TUI;
- a graphical history browser;
- a general branch manager;
- a replacement for Fork, Lazygit, or ordinary Git commands;
- a repository hosting client;
- a pull-request workflow;
- a multi-user collaboration system.

WipStream's purpose remains:

> Safely checkpoint, publish, retrieve, reconcile, recover, and complete one person's development work across multiple ordinary Git clones.

The Go CLI should remain narrowly focused on that workflow.

## 5. Git Implementation

The Go implementation MUST invoke the user's installed `git` executable.

It MUST NOT use:

- `go-git`;
- libgit2 bindings;
- another embedded Git implementation;
- a partial reimplementation of Git repository semantics.

The installed Git executable is authoritative for:

- commits;
- refs;
- index operations;
- worktree state;
- hooks;
- merges;
- repository configuration;
- credentials;
- transports;
- remote operations;
- atomic pushes;
- ref transactions;
- Git's own active-operation state.

The Go implementation should wrap Git execution behind a small repository/process abstraction so workflow code does not directly construct subprocesses everywhere.

Conceptually:

```text
CLI / prompts
      |
      v
WipStream workflows
      |
      v
repository / safety / operation abstractions
      |
      v
os/exec
      |
      v
git
```

The Git wrapper SHOULD expose semantic operations useful to WipStream rather than becoming a generic Go Git API.

## 6. Runtime Dependencies

The resulting `wipstream` executable MUST NOT depend on:

- VS Code;
- Node.js;
- npm;
- the TypeScript implementation;
- xmake;
- Python;
- another runtime environment.

The runtime dependency is principally the installed `git` executable.

The development and compatibility-test environment MAY use Node.js to execute the existing TypeScript implementation as the reference implementation.

That development dependency must not leak into the distributed Go program.

## 7. Go Tooling

Use the standard Go toolchain and Go modules.

The project SHOULD use ordinary commands such as:

```text
go build
go test ./...
go test -race ./...
go fmt ./...
go vet ./...
go mod tidy
```

Dependencies are managed with:

```text
go.mod
go.sum
```

Do not introduce xmake, Make, CMake, or another build system for the initial implementation.

Cross-platform builds should initially use normal Go cross-compilation through `GOOS` and `GOARCH`.

Additional release orchestration may be introduced later only if an actual need appears.

## 8. CLI Framework

Use Cobra for the CLI command hierarchy unless implementation experience reveals a concrete reason not to.

Cobra's responsibility is limited to:

- command dispatch;
- arguments;
- options;
- usage text;
- command help;
- shell completion if desired;
- conversion of returned errors into suitable process exit behavior.

Cobra MUST NOT contain the core WipStream workflow logic.

Workflow operations must remain callable without Cobra so they can be tested directly.

Conceptually:

```text
Cobra command
    |
    v
CLI interaction adapter
    |
    v
workflow function
    |
    v
repository operations
```

## 9. Interactive Terminal Input

Use `huh` where it provides clear value for:

- numbered or arrow-key selections;
- confirmation prompts;
- commit-message input;
- branch-name input;
- other small validated terminal dialogs.

The program does not require a persistent full-screen TUI.

WipStream's interaction model is fundamentally:

```text
inspect
prompt if necessary
preview
confirm if necessary
perform operation
report result
```

rather than a continuously displayed application UI.

The interaction library MUST remain outside the functional workflow layer.

Workflow functions should accept values, callbacks, or small interfaces representing required decisions.

### 9.1 Reprompting

Input-validation failures MAY reprompt.

Examples:

- blank required commit message;
- invalid branch name;
- invalid selection;
- malformed simple user input.

Repository or workflow-state failures MUST generally terminate the operation rather than becoming automatic retry loops.

Examples:

- divergent history;
- dirty submodule;
- parent update required;
- another Git operation is active;
- remote changed after inspection;
- unsafe deletion;
- unresolved merge.

These are WipStream state/refusal conditions, not invalid terminal input.

The CLI should preserve the same semantic distinction currently made by the extension.

## 10. Public Command Surface

The Go CLI MUST expose the same WipStream operations currently exposed through the VS Code Command Palette.

Initial command mapping:

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

These correspond to:

```text
Initialize Repository
Get from Remote
Commit and Save
Start Branch
Finish Branch
Update from Parent
Reconcile with Remote
Continue Pending Merge
Abort Pending Merge
Recover Incomplete Operation
Undo Last Action
Condense Branch
```

Exact CLI spelling may be reviewed during implementation, but the Go version MUST NOT omit an operation merely because it appears uncommon or advanced.

Behavioral parity is the initial objective.

## 11. Repository-Local WipStream State

Every clone managed by WipStream may contain private WipStream state associated with that clone.

This state belongs to the managed target repository, not to the WipStream source repository.

For a project such as:

```text
epi_sim/
    .git/
    src/
    ...
```

WipStream may store local operational state within:

```text
epi_sim/.git/
```

The Go implementation MUST use these locations and conventions identically to the TypeScript implementation.

Current categories include:

### 11.1 Local Git configuration

Examples include:

```text
wipstream.remote
branch.<branch>.wipstreamParent
```

### 11.2 Private WipStream files in the common Git directory

Examples include:

```text
.git/wipstream/operations/<operation-id>.json
.git/wipstream/command.lock
```

The actual common Git directory MUST be determined using Git rather than assuming that it is literally `<working-tree>/.git`.

### 11.3 Private Git refs

Examples include:

```text
refs/wipstream/recovery/<operation-id>/<ordinal>
refs/wipstream/command-lock-recovery
```

These refs point into the repository's normal Git object database but are private WipStream implementation state.

They are not ordinary branches.

### 11.4 Clone locality

This private operational metadata is local to each clone.

It is not the mechanism by which two machines synchronize.

Cross-machine synchronization continues to occur through ordinary Git commits, branches, and the configured remote.

Therefore:

```text
machine A clone
    .git/wipstream/...

machine B clone
    .git/wipstream/...
```

contain independent local operational histories.

Compatibility of these private structures matters when the TypeScript extension and Go CLI are alternated on the SAME clone.

## 12. Persistent Compatibility Contract

The initial Go implementation MUST preserve compatibility with the current TypeScript implementation for:

- WipStream Git configuration keys;
- branch-parent configuration;
- operation receipt paths;
- operation receipt JSON schemas;
- schema-version handling;
- operation identifiers;
- mutation phases;
- operation status values;
- pending-merge representation;
- recovery outcomes;
- operation plans;
- recovery refs;
- recovery-ref naming;
- command locking;
- lock recovery;
- incomplete-operation detection;
- undo eligibility;
- remote-adoption metadata;
- checkout transitions;
- local and remote ref-update representations.

Persistent metadata is a protocol.

It MUST NOT be redesigned merely to make the Go implementation aesthetically cleaner.

Go structures should serialize and deserialize the established protocol rather than inventing a parallel representation.

## 13. Compatibility Principle

A repository operated by one implementation must remain valid input to the other.

The following sequence must be normal and supported:

```text
VS Code WipStream
      |
      v
Go WipStream
      |
      v
VS Code WipStream
      |
      v
Go WipStream
```

No migration command should be necessary.

No implementation-specific initialization should be necessary after the repository has already been initialized by the other implementation.

If Go encounters valid persistent state created by TypeScript and cannot safely continue, that is a compatibility bug.

The reverse is also true.

## 14. Testing Principle

Compatibility MUST be demonstrated, not inferred.

The existing TypeScript test suite contains valuable descriptions of WipStream behavior and should be treated as a source of test scenarios.

The Go implementation should reproduce those behavioral scenarios using Go tests and disposable repositories.

In addition, a dedicated interoperability test suite MUST exercise both implementations against the same disposable repositories.

### 14.1 Round-trip testing

Important tests should explicitly alternate implementations:

```text
TypeScript
    -> Go
    -> TypeScript
```

and:

```text
Go
    -> TypeScript
    -> Go
```

Examples include:

```text
TS initialize
Go start
TS save
Go get
TS update
Go finish
TS get
```

The important result is not that the implementations execute identical subprocess sequences.

The important result is that each operation leaves repository and WipStream state that the other implementation correctly understands.

### 14.2 Recovery round trips

Interrupted-operation behavior requires especially strong interoperability tests.

Examples:

```text
TypeScript creates operation receipt and recovery refs
Go detects and recovers the operation
TypeScript successfully performs the next operation
```

and:

```text
Go creates operation receipt and recovery refs
TypeScript detects and recovers the operation
Go successfully performs the next operation
```

The same principle applies to undo, interrupted merges, command locks, and incomplete operations.

## 15. Test Isolation

Automated tests MUST NEVER operate on an existing real development repository.

All workflow/integration tests must create their own disposable fixtures.

A typical fixture should contain:

```text
temporary-root/
    fixture-marker
    remote.git/
    first/
    second/
```

where:

- `remote.git` is a newly created local bare remote;
- `first` and `second` are ordinary newly created clones;
- all mutations remain beneath the temporary test root.

Before destructive test operations, the harness SHOULD verify that:

1. the fixture root was created by the current test machinery;
2. a test sentinel exists;
3. all target paths resolve beneath that fixture root;
4. no source repository is being used as a workflow target.

Tests involving:

- ref rewriting;
- branch deletion;
- remote adoption;
- file replacement;
- recovery;
- undo;

must obey this isolation requirement.

## 16. Relationship to the Existing TypeScript Tests

The existing TypeScript test suite should be reused conceptually as much as practical.

The primary things to preserve are:

- fixture scenarios;
- expected repository states;
- refusal conditions;
- operation sequences;
- failure injection points;
- recovery expectations;
- safety invariants.

Literal JavaScript test implementation reuse is not required.

The Go test suite should be idiomatic Go.

A separate compatibility harness may invoke the compiled TypeScript functional implementation through Node during development.

VS Code itself is NOT required for Go compatibility testing.

## 17. Architectural Guidance

The Go implementation should preserve conceptual separation between:

```text
CLI / terminal interaction
workflow logic
repository model
repository safety
operation planning
operation receipts
recovery
undo
Git execution
```

The exact package structure should be chosen idiomatically in Go.

A possible organization is:

```text
cmd/wipstream/

internal/cli/
internal/git/
internal/repository/
internal/workflow/
internal/operations/
internal/safety/
```

This is guidance rather than a mandatory directory layout.

The important rule is:

> Cobra and terminal-interaction code must not become dependencies of the workflow implementation.

Likewise:

> Git subprocess mechanics should be centralized sufficiently that workflow code expresses WipStream semantics rather than repeated command-line plumbing.

## 18. Porting Strategy

The port SHOULD proceed incrementally.

A wholesale translation of the TypeScript codebase followed by compatibility debugging is discouraged.

Recommended sequence:

1. Inventory the persistent protocol and current behavioral invariants.
2. Implement Go representations for existing persistent metadata.
3. Prove TypeScript-to-Go and Go-to-TypeScript metadata round trips.
4. Implement the Git subprocess/repository layer.
5. Port repository-state inspection.
6. Port safety checks.
7. Port operation planning and receipts.
8. Port command locking and recovery refs.
9. Port one relatively simple workflow.
10. Establish cross-implementation workflow tests.
11. Port remaining workflows individually.
12. Port recovery and undo.
13. Add the complete Cobra command surface.
14. Add terminal prompts and validation.
15. Run comprehensive TS -> Go -> TS and Go -> TS -> Go scenarios.

Every major subsystem should achieve compatibility before the port proceeds far beyond it.

## 19. Non-Goals for the Initial Port

The initial implementation should NOT attempt to:

- redesign WipStream's metadata;
- remove existing safety checks;
- simplify recovery semantics;
- add general Git history editing;
- add arbitrary interactive rebasing;
- become a Lazygit replacement;
- add a full-screen TUI;
- use an embedded Git library;
- optimize for runtime performance;
- minimize binary size;
- eliminate Node from the compatibility-test environment;
- make the Go and TypeScript internal source structures match;
- introduce a new build system.

New features can be considered after interoperability and behavioral parity are established.

## 20. Definition of Initial Success

The initial Go port is successful when:

1. `wipstream` is a standalone Go executable.
2. It implements the full existing public WipStream command surface.
3. It requires only Git at runtime.
4. Existing WipStream-initialized clones can be used immediately.
5. The TypeScript extension can use clones previously operated by Go.
6. Go can use clones previously operated by the TypeScript extension.
7. Operation receipts, recovery state, undo state, parent metadata, and private refs remain interoperable.
8. Round-trip compatibility tests pass in both directions.
9. Existing safety behavior has not materially weakened.
10. Tests operate entirely on disposable fixtures.
11. The implementation is reasonably idiomatic and readable Go rather than a transliteration of TypeScript.