# WipStream Go

A standalone Go CLI for the WipStream workflow, using the installed Git
executable and the same clone-local configuration, receipts, locks, and recovery
refs as [the VS Code extension](https://github.com/lewisl/wip-stream).

This is an initial compatibility implementation. Use disposable repositories
while evaluating it; the fixture and interoperability suites cover the tested
behaviors, and further review is appropriate before using history-changing
commands on important work.

## Build and use

Requires Go 1.27.1 or later and Git. Node and VS Code are development/test tools
only; the executable does not invoke them.

```sh
go build -o bin/wipstream ./cmd/wipstream
bin/wipstream --help
cd /path/to/ordinary/clone
/path/to/wipstream init
/path/to/wipstream start feature
/path/to/wipstream save -m "Checkpoint work"
/path/to/wipstream finish --disposition retain --yes
```

Commands: `init`, `get`, `save`, `start`, `finish`, `update`,
`reconcile`, `continue`, `abort`, `recover`, `undo`, and `condense`.
Terminal prompts use huh; every prompt has a flag for noninteractive use.
Use `-C /path/to/clone` to select a repository and `--json` for structured
results. Noninteractive runs refuse missing input instead of assuming approval.

Initialization covers all branches. If an authority choice is needed, select
`--authority local-work`, `remote`, `reconcile`, or `cancel`.
Remote replacement requires either `--backup-parent /existing/folder` for a
verified complete project copy, or explicit `--discard-local-work`.
Ignored files are preserved; collisions with remote paths prevent replacement.

`finish` requires `--disposition retain|delete`; imported branches need an
explicit `--parent`. `finish`, `condense`, `undo`, and `recover` require
confirmation, supplied by `--yes` in scripts. `condense` also requires a
message. `recover --operation ID --yes` keeps current files and refs and
closes an incomplete attempt; it does not replay or claim a completed handoff.

Save checkpoints may remain local when publication fails. An unsuccessful
handoff returns a nonzero exit status. Do not resume that work in another clone
until a subsequent handoff succeeds.

## Validation

Go tests are in `*_test.go` files beside their packages under `internal/`.
They test the Go app using disposable Git repositories. Run `go test ./...`
or `go test -v ./...` to see individual tests and subtests; neither command
requires Node or VS Code. See [the Go testing guide](docs/testing.md) for the
command-by-command coverage and its correspondence to the reference scenarios.

```sh
go test -race ./...
go vet ./...
go build -o bin/wipstream ./cmd/wipstream
WIPSTREAM_REFERENCE=/path/to/compiled/wip-stream node test/interop.js
```

The reference checkout must have its `out/` files compiled with its own
`npm ci` and `npm run compile`. An unpacked installed VSIX containing
`out/` can also serve as the reference. The interoperability runner uses only
Node built-ins and creates disposable local bare remotes and ordinary clones.
It tests both directions, including pending merges, recovery, receipts, and
cross-implementation Undo. Real project checkouts are never workflow fixtures.

For the real VS Code roundtrip, install VS Code, provide a graphical session
(Xvfb works on Linux), and obtain the VSIX from the reference repository:

```sh
WIPSTREAM_VSIX=/path/to/wip-stream/dist/lewisl.wipstream-0.2.9.vsix \
  node test/vscode-roundtrip.js
```

Set `WIPSTREAM_CODE` if the VS Code CLI is outside PATH, and
`WIPSTREAM_GO_BINARY` if the compiled CLI is outside `bin/wipstream`.
The runner installs the VSIX into a new isolated profile and runs its public
commands in VS Code's extension host. Native input acceptance is automated.
It records the current run in `test-results/vscode-roundtrip.json`.
In cloud containers that cannot run Chromium's nested sandbox, set
`WIPSTREAM_CODE_NO_SANDBOX=1`. The extension itself is the unchanged VSIX.

See [validation results](docs/validation.md) for the tested versions and sequence.

Windows lock owners are conservatively treated as unverifiable for stale-lock
recovery; ordinary lock interoperability is supported. Current behavioral
validation runs on Linux.

See `AGENTS.md` and `design/` for the compatibility contract.
