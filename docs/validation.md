# Validation evidence

Validated on Linux amd64 with Go 1.27.1, Git 2.52.0, Node 24.19.0,
VS Code 1.108.0, and the repository-provided WipStream 0.2.9 VSIX.

The reference implementation was checked out at
`a2193144d02d38838d3f610610324c7b5f9e64e0`.
The VSIX SHA-256 is
`5fab70590a6fde378991df84b6acc5de2eea2fe166e99eb832ec8d34feb212c4`.
VS Code and Xvfb were installed in the cloud container; no local-machine editor
or real development repository was used.

Checks passed:

- `go build ./...`
- `go test -race ./...`: operation reader tests and thirteen Git fixture scenarios.
- `go vet ./...`
- `node test/interop.js`: fourteen cross-implementation scenarios, against
  the installed VSIX's compiled functional implementation.
- The reference implementation's full test runner: 25 scripts passed,
  zero skipped. The process umask was set to 022, as required by its explicit
  directory-permission assertions.
- `node test/vscode-roundtrip.js`: passed three times, including after the final
  safety changes and rebuild.

## Extension → Go → extension

The test installed the unchanged GitHub VSIX into a fresh VS Code profile,
activated it in the real extension host, and verified registration of all twelve
public WipStream commands. It drove native quick-input acceptance for the
default remote, branch name, and checkpoint message.

On a disposable clone with a disposable local bare remote, it executed:

1. VS Code `wipstream.init`.
2. VS Code `wipstream.start`.
3. VS Code `wipstream.saveup`, publishing extension-created files.
4. Go `get`.
5. Go `save`, publishing Go-created files.
6. VS Code `wipstream.resume`, reading Go-created state.
7. VS Code `wipstream.saveup`, publishing further extension work.
8. Go `finish --disposition retain --yes`.
9. VS Code `wipstream.resume`.

The final checkout was clean on `main`. Both implementations' file contents
were present in the expected commits. The extension read ten operation receipts,
including the completed Go Save receipt, and found no incomplete operations.
The host exited with status zero and wrote a result for that invocation.

## Functional interoperability

The fourteen independent scenarios cover both initialization directions, dirty
Start, completed receipts, Condense and Undo in both directions, pending Update
Continue/Abort, pending Reconcile Continue/Abort, explicit recovery in both
directions, TypeScript lock refusal by Go, Finish deletion followed by
TypeScript Undo, and exact binary/deleted/whitespace checkpoint restoration.

These are tested scenarios, not proof of every possible platform or concurrent
failure state. Native keyboard and visual command-palette inspection are outside
this automated suite. Windows/macOS runtime behavior has not been validated.
