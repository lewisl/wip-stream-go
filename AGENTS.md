# AGENTS.md

## Mandatory First Read

**Read `design/overall_spec.md` in full before doing implementation work in this repository.** It is the primary project specification and is required context for architecture, behavior, compatibility, persistence, testing, and scope decisions.

This `AGENTS.md` summarizes operating rules for agents; it does not replace `design/overall_spec.md`. If this file and `design/overall_spec.md` appear to conflict, follow `design/overall_spec.md` unless the user explicitly directs otherwise.

For exact WipStream semantics not fully specified there, inspect the existing TypeScript WipStream implementation and its tests at https://github.com/lewisl/wip-stream. Treat that repository as the behavioral and persistent-format reference for the port.

## Project Purpose

`wip-stream-go` is a standalone Go implementation of WipStream, the existing VS Code extension at https://github.com/lewisl/wip-stream.

This is a compatibility port, not a redesign. The Go CLI and the TypeScript extension must be able to operate alternately on the same Git clone without migration, repair, or implementation-specific setup.

## Core Compatibility Rule

A repository state produced by either implementation must remain valid input to the other.

Compatibility includes more than ordinary Git history. The Go implementation must preserve the existing WipStream clone-local protocol, including as applicable:

- local Git configuration such as `wipstream.remote`;
- branch parent metadata such as `branch.<name>.wipstreamParent`;
- private files under the repository's common Git directory, including `wipstream/operations/*.json`;
- operation receipt schemas and schema versions;
- private refs under `refs/wipstream/*`;
- recovery refs and recovery semantics;
- command locking and lock recovery;
- incomplete-operation detection;
- pending-merge state;
- undo eligibility and undo behavior.

Do not redesign persistent metadata during the initial port. If Go and TypeScript disagree, presume the Go implementation is wrong until the discrepancy is investigated.

The TypeScript source defines required behavior and persistent formats, but it does not dictate Go package structure or coding style.

## Runtime and Git Rules

The shipped `wipstream` executable must have no runtime dependency on VS Code, Node.js, npm, Python, xmake, or the TypeScript implementation.

Use the installed `git` executable for Git operations via `os/exec` / `exec.CommandContext`.

Do **not** introduce:

- `go-git`;
- libgit2 bindings;
- another embedded Git implementation;
- a partial reimplementation of Git semantics.

Git itself remains authoritative for commits, refs, index state, worktree state, merges, hooks, credentials, transports, configuration, remote operations, atomic pushes, and active Git operations.

Centralize subprocess handling enough that workflow code expresses WipStream behavior rather than repeating raw process plumbing everywhere.

## Go Project Conventions

Use ordinary Go tooling only:

```sh
go build ./...
go test ./...
go test -race ./...
go fmt ./...
go vet ./...
go mod tidy
```

Use Go modules (`go.mod`, `go.sum`) for dependencies. Do not add xmake, Make, CMake, or another build system unless a concrete later requirement justifies it.

Prefer idiomatic, readable Go over transliterating TypeScript patterns. This repository is also intended to be understandable to someone learning Go.

Keep packages focused and avoid unnecessary abstraction. Performance, executable size, and memory usage are not important goals for this program; correctness, safety, interoperability, and clarity are.

## CLI and Interaction

The executable is `wipstream`.

The public CLI should correspond to the WipStream commands exposed by the VS Code extension:

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

Use Cobra for command structure unless a concrete implementation issue shows it is unsuitable.

Use `huh` where it is useful for small interactive selections, confirmations, and text entry. Do not build a persistent full-screen TUI for v1.

Keep Cobra and terminal-prompt code out of the functional workflow layer. Workflow functions must be directly testable without a terminal.

Input validation may reprompt. Repository-state and workflow failures generally must terminate with the same refusal semantics as the TypeScript implementation rather than being converted into retry loops.

Do not expand WipStream into a general Git client. Branch browsing, arbitrary rebasing, history editing, and similar generic Git features are outside the initial scope unless already part of WipStream behavior.

## Safety Requirements

WipStream performs destructive and history-changing operations. Preserve the existing safety model before adding convenience.

Never weaken a refusal, lease check, expected-object check, recovery guard, or dirty-state check merely to make a workflow easier to implement.

Do not mutate the reference TypeScript repository while using it as an oracle unless the task explicitly requires a coordinated change to both implementations.

### Test isolation is mandatory

Automated workflow, integration, recovery, undo, ref-rewrite, branch-deletion, remote-adoption, and interoperability tests must operate only on disposable fixtures created by the test run.

Tests must not use any existing real project as a WipStream target.

Prefer fixtures containing a newly created temporary root, local bare remote, and ordinary clones. For destructive tests, verify that the target paths resolve beneath the temporary fixture root and, where practical, require a fixture sentinel before mutation.

Source repositories may be read, built, and inspected during testing, but they are never workflow fixtures.

## Testing Strategy

The existing TypeScript WipStream tests are a source of behavioral scenarios and invariants. Reuse their cases and expectations conceptually, while writing idiomatic Go tests.

Use disposable local Git remotes and ordinary clones. Tests should verify observable repository state, WipStream metadata, refusal conditions, and recovery behavior rather than merely checking command output.

Interoperability must be demonstrated in both directions:

```text
TypeScript -> Go -> TypeScript
Go -> TypeScript -> Go
```

Important compatibility tests include:

- initialization followed by operations from the other implementation;
- save/get/start/update/finish sequences across implementations;
- receipts written by one implementation and read by the other;
- interrupted operations recovered by the other implementation;
- undo across implementation boundaries;
- pending merge continuation/abort across implementation boundaries;
- private refs and branch-parent metadata consumed by both implementations.

Node.js may be used in development or compatibility tests to invoke the TypeScript functional implementation. VS Code is not a dependency of the Go port or of its core compatibility tests.

Do not require byte-for-byte equality for incidental diagnostics or subprocess ordering unless the protocol or tests require it. Require equivalence of persisted formats, safety properties, operation meaning, and resulting state.

## Implementation Approach

Port incrementally. Do not translate the entire TypeScript tree first and attempt to reconcile behavior afterward.

A preferred sequence is:

1. inventory persistent formats and invariants;
2. model existing metadata in Go;
3. prove metadata read/write compatibility;
4. implement the Git subprocess and repository layer;
5. port repository inspection and safety checks;
6. port operation plans, receipts, locks, and recovery refs;
7. port one workflow and establish cross-implementation testing;
8. port remaining workflows one at a time;
9. port recovery and undo;
10. complete the Cobra command surface and terminal interaction;
11. run broad round-trip compatibility scenarios.

At each stage, prefer a small compatible slice over a broad approximate port.

## Source of Truth and Change Discipline

Before implementing a workflow or persistent structure:

1. read the relevant section of `design/overall_spec.md`;
2. inspect the corresponding TypeScript source;
3. inspect the relevant TypeScript tests;
4. identify persistent state and safety invariants;
5. implement the smallest compatible Go slice;
6. add or port tests before moving to the next workflow.

Do not guess about WipStream behavior when the reference implementation can answer the question.

Do not silently fix an apparent TypeScript bug while porting. Document the discrepancy and keep compatibility unless a deliberate change to both implementations is agreed upon.

## Expected Architecture

Exact directories are not prescribed, but preserve separation among these concerns:

- CLI / terminal interaction;
- workflow logic;
- Git execution and repository inspection;
- repository safety;
- operation planning and receipts;
- recovery;
- undo.

A reasonable starting shape is:

```text
cmd/wipstream/
internal/cli/
internal/git/
internal/repository/
internal/workflow/
internal/operations/
internal/safety/
```

Use a different layout if it is more idiomatic after implementation experience. Architectural boundaries matter more than matching these names.

## Definition of Done for a Ported Behavior

A workflow is not considered ported merely because the Go command succeeds on a happy path.

For each behavior, verify as applicable:

- success state matches the reference implementation;
- refusal conditions are preserved;
- local and remote refs are correct;
- worktree and index behavior are correct;
- relevant Git config is correct;
- receipts and private refs are compatible;
- interrupted-operation behavior is recoverable;
- the TypeScript implementation can continue from the resulting clone;
- the Go implementation can continue from equivalent TypeScript-produced state;
- tests use only disposable fixtures.

## Working Style for Agents

Keep changes narrowly scoped. Run the smallest relevant tests while developing, then the broader Go and interoperability suites before declaring a subsystem complete.

Prefer inspecting existing code and tests over inventing new semantics. Explain any compatibility-sensitive assumption in code comments only where the reason would otherwise be difficult to reconstruct.

Do not add dependencies, frameworks, build tools, or architectural layers without a concrete need.

When a task uncovers an ambiguity in the protocol or a possible bug in the TypeScript reference, stop short of silently choosing a new behavior. Record the finding clearly so the compatibility decision can be made deliberately.
