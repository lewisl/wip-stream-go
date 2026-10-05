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
