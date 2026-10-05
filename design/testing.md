# Testing the Go app

Go tests live beside the package they test. A file named `save_test.go` can
contain several functions named `TestSave...`. A function can use `t.Run` to
create named subtests, such as `TestStartRefusals/remote-existing`. Go discovers
these automatically; the directory does not have to be named `test`.

Run these commands from the repository root:

```sh
go test ./...                          # all Go tests
go test -v ./...                       # show each test and subtest
go test -list . ./...                  # list top-level tests
go test ./internal/workflow -run TestSave -v
go test ./internal/workflow -run 'TestStartRefusals/remote-existing' -v
go test ./internal/cli -v              # actual Cobra command handlers
go test -race ./...                    # include Go's race detector
```

A successful repeat may say `(cached)`. Add `-count=1` to force a fresh run.
These commands need Go and Git. They do not run JavaScript or start VS Code.

## Where to find the tests

| Go test file | Behavior checked | Reference scenarios |
| --- | --- | --- |
| `internal/cli/cli_test.go` | All twelve registered commands and their flags; actual CLI dispatch, JSON results, pending merges, missing input, help, and failed Save diagnostics | `command-surface`, `command-handlers`, `public-command-sequences` |
| `internal/workflow/init_test.go` | Initialization without a remote HEAD cache, authority choices, local checkpoints, remote replacement approval, verified backup, ignored files, and changed approval state | `initialize-repository`, `setup-workflow`, `remote-adoption` |
| `internal/workflow/get_test.go` | All-branch retrieval, remote-only branches, safe deletion, checkout fallback, and all-or-nothing refusal for unpublished or divergent work | `get-from-remote` |
| `internal/workflow/save_test.go` | Publication across branches, imported remote changes, no-op Save, retained offline/divergent checkpoints, rejected commit hooks, and work changed during a message prompt | `commit-and-save` |
| `internal/workflow/start_test.go` | Staged, unstaged, and untracked work carried without committing; parent metadata; invalid/existing names and detached HEAD refusals | `lifecycle-workflow`, `workflow-refusals` |
| `internal/workflow/finish_test.go` | Retain/delete, parent advancement, configuration cleanup, Undo, and refusal before checkpointing or confirmation | `lifecycle-workflow`, `undo-workflow` |
| `internal/workflow/update_test.go` | Local parent merge, Undo, explicit imported parent confirmation, and dirty-work refusal | `lifecycle-workflow`, `undo-workflow` |
| `internal/workflow/condense_test.go` | Exact tree preservation, one parent/commit, Undo, cancellation and stale approval, and another clone's old history | `lifecycle-workflow`, `workflow-refusals` |
| `internal/workflow/reconcile_test.go` | Merge and publish both histories; refuse non-divergent histories or another divergent branch | `conflict-workflow`, `workflow-refusals` |
| `internal/workflow/merge_test.go` | Continue/Abort restoration, unresolved conflict and mismatched merge refusal, and external Git completion/abort recognition | `conflict-workflow`, `recovery-workflow` |
| `internal/workflow/recover_test.go` | Explicit operation selection, cancellation, keeping staged/unstaged/untracked work and refs, and active merge refusal | `recovery-workflow` |
| `internal/workflow/undo_test.go` | Undo Get including imported configuration, exact binary/deleted/whitespace checkpoint restoration, and later-work/checkout/configuration/recovery refusals | `undo-workflow` |
| `internal/workflow/safety_test.go` | Malformed receipts, command locking, and incomplete-operation refusal across ordinary commands | `repository-safety`, `receipt-reader` |
| `internal/git/repository_test.go` | Raw byte preservation, atomic local ref transactions, and exact remote leases including previously absent branches | `git`, `commit-and-save` |
| `internal/operations/operations_test.go` | Receipt schema/ref validation and legacy exact-lease normalization | `receipt-reader`, `operations` |

Reference names refer to `test/<name>.test.js` in
[lewisl/wip-stream](https://github.com/lewisl/wip-stream), examined at commit
`a2193144d02d38838d3f610610324c7b5f9e64e0`. The scenarios are implemented in Go;
these Go tests do not invoke the extension. This mapping covers the scenarios
listed above, rather than claiming every reference case has been ported. The
reference's complete lifecycle interruption matrix, platform-specific recovery,
and every backup/recovery edge case still have broader coverage in that suite.

## Fixture isolation and assertions

Each repository test creates a fresh `t.TempDir()` root containing a fixture
marker, a local bare remote, and ordinary repositories or clones. Git helpers
resolve target paths and require the fixture marker before executing Git.
Neither source checkout is a WipStream target. Go removes temporary fixtures
when each test finishes.

Success assertions inspect Git refs, committed file contents, checkout and
index/worktree state, configuration, receipts, and recovery refs as applicable.
Refusal tests independently capture file bytes and modes, staged/unstaged
changes, local/private refs, remote branches, configuration, and receipt bytes
before and after the command. A permitted fetch may update remote tracking refs;
those are excluded from the refusal snapshot.

`internal/workflow/fixture_test.go` creates workflow fixtures;
`state_test.go` contains the independent state assertions. CLI tests invoke the
real Cobra handlers with arguments and flags against their own fixtures.
The executable commit-hook test is skipped on Windows because it uses a POSIX
shell hook. Runtime validation has been performed on Linux.

## Existing interoperability evidence

The JavaScript files under `test/` provide the separate cross-implementation
and cloud VS Code roundtrip harnesses. They demonstrated the earlier
extension → Go → extension sequence and shared-receipt compatibility.
Their invocation is optional when running the Go command suite above.
See [validation evidence](validation.md) for the recorded runs.

## Build and review checks

Run the standard Go tooling from the repository root:

```sh
go build ./...
go vet ./...
go test -race ./...
go build -o bin/wipstream ./cmd/wipstream
```

Read [AGENTS.md](../AGENTS.md), the [port specification](overall_spec.md),
[command semantics](command_spec.md), and [persistence protocol](persistence_protocol.md)
before changing workflow behavior. Write contributor documentation in `design/`
and user guides in `doc/`; keep the root README focused on using the app.

## Functional interoperability

The compatibility harness alternates the Go executable and the reference's
compiled functional implementation in disposable local bare remotes and clones:

```sh
WIPSTREAM_REFERENCE=/path/to/compiled/wip-stream node test/interop.js
```

The reference checkout must have its `out/` files compiled with its own
`npm ci` and `npm run compile`. An unpacked installed VSIX containing `out/`
can also serve as the reference. The harness uses Node built-ins. It exercises
both directions, including pending merges, recovery, receipts, and Undo.
Neither source repository may be used as a workflow fixture.

The runner defaults to `bin/wipstream`; set `WIPSTREAM_GO_BINARY` if the executable
is elsewhere. Build it before running the harness.

## Cloud VS Code roundtrip harness

The separate host harness requires VS Code and a graphical session; Xvfb works
on Linux. Obtain the unchanged VSIX from the reference repository:

```sh
WIPSTREAM_VSIX=/path/to/wip-stream/dist/lewisl.wipstream-0.2.9.vsix \
  node test/vscode-roundtrip.js
```

The runner installs the VSIX into a fresh isolated profile and exercises public
commands in the real extension host. It creates a disposable project fixture
and records the invocation in `test-results/vscode-roundtrip.json`.
Set `WIPSTREAM_CODE` if the Code CLI is outside PATH, and
`WIPSTREAM_GO_BINARY` if the compiled Go CLI is outside `bin/wipstream`.
For cloud containers that cannot run Chromium's nested sandbox, set
`WIPSTREAM_CODE_NO_SANDBOX=1`. `WIPSTREAM_KEEP_FIXTURE=1` retains the disposable
fixture for inspection; the default cleans it up. The installed extension is the
unchanged GitHub VSIX.

These host and interoperability harnesses are separate from `go test ./...`.
See [validation evidence](validation.md) for previously recorded versions and
results.
