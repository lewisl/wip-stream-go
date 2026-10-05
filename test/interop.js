// Run with WIPSTREAM_REFERENCE pointing at a compiled lewisl/wip-stream checkout.
// Every WipStream target is a fresh ordinary clone beneath a sentinel fixture.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const reference = process.env.WIPSTREAM_REFERENCE;
if (!reference) throw Error("Set WIPSTREAM_REFERENCE to the compiled TypeScript reference checkout");
const binary = path.resolve(process.env.WIPSTREAM_GO_BINARY || "bin/wipstream");
const { GitRepository } = require(path.join(reference, "out/git"));
const sync = require(path.join(reference, "out/generalized-workflow"));
const lifecycle = require(path.join(reference, "out/lifecycle-workflow"));
const conflict = require(path.join(reference, "out/conflict-workflow"));
const operations = require(path.join(reference, "out/operations"));
const undo = require(path.join(reference, "out/undo-workflow"));
const recovery = require(path.join(reference, "out/recovery-workflow"));
const safety = require(path.join(reference, "out/repository-safety"));
function git(dir, args) {
  const r = spawnSync("git", args, { cwd: dir, encoding: "utf8", env: { ...process.env, GIT_CONFIG_GLOBAL: os.devNull, GIT_CONFIG_NOSYSTEM: "1" } });
  assert.equal(r.status, 0, r.stderr || r.error); return r.stdout.trim();
}
function go(dir, args, expected = 0) {
  const r = spawnSync(binary, ["-C", dir, "--json", ...args], { encoding: "utf8" });
  assert.equal(r.status, expected, r.stderr || r.stdout || r.error);
  return expected === 0 ? JSON.parse(r.stdout) : r.stderr;
}
function write(dir, name, value) { fs.writeFileSync(path.join(dir, name), value); }
function commit(dir, name, value) { write(dir, name, value); git(dir, ["add", "--all"]); git(dir, ["commit", "-m", name]); }
async function fixture(run) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "wipstream-interop-"));
  write(root, "fixture-marker", "disposable");
  const remote = path.join(root, "remote.git"), seed = path.join(root, "seed");
  git(root, ["init", "--bare", "--initial-branch=main", remote]);
  git(root, ["init", "--initial-branch=main", seed]);
  git(seed, ["config", "user.name", "Fixture"]); git(seed, ["config", "user.email", "fixture@example.invalid"]);
  commit(seed, "main.txt", "base\n"); git(seed, ["remote", "add", "origin", remote]); git(seed, ["push", "origin", "main"]);
  async function clone(name) {
    const dir = path.join(root, name);
    assert.ok(dir.startsWith(root + path.sep) && fs.existsSync(path.join(root, "fixture-marker")));
    git(root, ["clone", remote, dir]); git(dir, ["config", "user.name", "Fixture"]); git(dir, ["config", "user.email", "fixture@example.invalid"]);
    return { dir, repo: await GitRepository.open(dir) };
  }
  try { await run({ root, remote, clone }); } finally { fs.rmSync(root, { recursive: true, force: true }); }
}
async function saved(repo) { const r = await sync.commitAndSave(repo); assert.equal(r.published, true, r.message); }
async function conflictFixture(clone) {
  const a = await clone("a"), b = await clone("b");
  await sync.initializeRepository(a.repo); await sync.initializeRepository(b.repo);
  await lifecycle.startBranch(a.repo, "feature"); write(a.dir, "main.txt", "feature\n"); await saved(a.repo);
  write(b.dir, "main.txt", "parent\n"); await saved(b.repo); return { a, b };
}
const cases = [
  ["TypeScript -> Go -> TypeScript branch lifecycle and receipts", async ({ clone }) => {
    const a = await clone("a"); await sync.initializeRepository(a.repo);
    go(a.dir, ["start", "feature"]); write(a.dir, "work.txt", "TS checkpoint\n"); await saved(a.repo);
    go(a.dir, ["get"]); write(a.dir, "go.txt", "Go checkpoint\n"); go(a.dir, ["save", "-m", "Go checkpoint"]);
    const receipts = await operations.listOperationReceipts(a.repo); assert.ok(receipts.some(r => r.plan.command === "Commit and Save" && r.plan.checkpoint?.message === "Go checkpoint"));
    go(a.dir, ["finish", "--disposition", "retain", "--yes"]);
    await sync.getFromRemote(a.repo); assert.equal(await a.repo.currentBranch(), "main");
    assert.equal(git(a.dir, ["show", "main:go.txt"]), "Go checkpoint");
  }],
  ["Go -> TypeScript -> Go initialization and dirty Start", async ({ clone }) => {
    const a = await clone("a"); go(a.dir, ["init"]); write(a.dir, "dirty.txt", "carry\n");
    await lifecycle.startBranch(a.repo, "feature"); go(a.dir, ["save", "-m", "carried"]);
    await sync.getFromRemote(a.repo); assert.equal(git(a.dir, ["show", "feature:dirty.txt"]), "carry");
    assert.equal((await operations.listOperationReceipts(a.repo)).every(r => !["planned", "in-progress"].includes(r.status)), true);
  }],
  ["Go condense -> TypeScript undo -> Go", async ({ clone }) => {
    const a = await clone("a"); go(a.dir, ["init"]); go(a.dir, ["start", "feature"]);
    for (const value of ["one", "two"]) { write(a.dir, "work.txt", value); go(a.dir, ["save", "-m", value]); }
    const before = git(a.dir, ["rev-parse", "HEAD"]); go(a.dir, ["condense", "-m", "condensed", "--yes"]);
    assert.equal((await undo.inspectUndoEligibility(a.repo)).eligible, true);
    await undo.undoLastAction(a.repo); assert.equal(git(a.dir, ["rev-parse", "HEAD"]), before); go(a.dir, ["get"]);
  }],
  ["TypeScript condense -> Go undo -> TypeScript", async ({ clone }) => {
    const a = await clone("a"); await sync.initializeRepository(a.repo); await lifecycle.startBranch(a.repo, "feature");
    for (const value of ["one", "two"]) { write(a.dir, "work.txt", value); await saved(a.repo); }
    const before = git(a.dir, ["rev-parse", "HEAD"]);
    await lifecycle.condenseBranch(a.repo, { confirmPreview: async () => true, requestMessage: async () => "condensed" });
    go(a.dir, ["undo", "--yes"]); assert.equal(git(a.dir, ["rev-parse", "HEAD"]), before); await sync.getFromRemote(a.repo);
  }],
  ["TypeScript pending Update -> Go Continue -> TypeScript", async ({ clone }) => {
    const { a } = await conflictFixture(clone); assert.equal((await lifecycle.updateFromParent(a.repo)).pending, true);
    write(a.dir, "main.txt", "resolved\n"); git(a.dir, ["add", "main.txt"]); go(a.dir, ["continue"]);
    await sync.getFromRemote(a.repo); assert.equal(git(a.dir, ["show", "HEAD:main.txt"]), "resolved");
  }],
  ["Go pending Update -> TypeScript Abort -> Go", async ({ clone }) => {
    const { a } = await conflictFixture(clone); const before = git(a.dir, ["rev-parse", "HEAD"]);
    assert.equal(go(a.dir, ["update"]).pending, true); await conflict.abortPendingMerge(a.repo);
    assert.equal(git(a.dir, ["rev-parse", "HEAD"]), before); go(a.dir, ["get"]);
  }],
  ["Go pending Update -> TypeScript Continue -> Go", async ({ clone }) => {
    const { a } = await conflictFixture(clone); assert.equal(go(a.dir, ["update"]).pending, true);
    write(a.dir, "main.txt", "resolved TS\n"); git(a.dir, ["add", "main.txt"]); await conflict.continuePendingMerge(a.repo); go(a.dir, ["get"]);
  }],
  ["TypeScript pending Reconcile -> Go Abort -> TypeScript", async ({ clone }) => {
    const a = await clone("a"), b = await clone("b"); await sync.initializeRepository(a.repo); await sync.initializeRepository(b.repo);
    commit(a.dir, "main.txt", "local\n"); write(b.dir, "main.txt", "remote\n"); await saved(b.repo);
    assert.equal((await conflict.reconcileWithRemote(a.repo)).pending, true); go(a.dir, ["abort"]);
    assert.equal(git(a.dir, ["show", "HEAD:main.txt"]), "local"); assert.equal((await operations.inspectIncompleteOperations(a.repo)).length, 0);
  }],
  ["Go pending Reconcile -> TypeScript Continue -> Go", async ({ clone }) => {
    const a = await clone("a"), b = await clone("b"); go(a.dir, ["init"]); await sync.initializeRepository(b.repo);
    commit(a.dir, "main.txt", "local\n"); write(b.dir, "main.txt", "remote\n"); await saved(b.repo);
    assert.equal(go(a.dir, ["reconcile"]).pending, true); write(a.dir, "main.txt", "resolved\n"); git(a.dir, ["add", "."]);
    await conflict.continuePendingMerge(a.repo); go(a.dir, ["get"]);
  }],
  ["TypeScript interruption -> Go recover preserves work -> TypeScript", async ({ clone }) => {
    const a = await clone("a"); await sync.initializeRepository(a.repo);
    const plan = operations.createOperationPlan({ command: "Get from Remote", checkout: { before: "main", after: "main" } });
    await operations.beginOperation(a.repo, plan); await operations.recordOperationPhase(a.repo, plan.operationId, "before-remote-fetch");
    write(a.dir, "dirty.txt", "kept"); go(a.dir, ["recover", "--yes"]); assert.equal(fs.readFileSync(path.join(a.dir, "dirty.txt"), "utf8"), "kept"); await saved(a.repo);
  }],
  ["Go interruption -> TypeScript recover -> Go", async ({ clone }) => {
    const a = await clone("a"); go(a.dir, ["init"]); write(a.dir, "work.txt", "checkpoint");
    const hook = path.join(a.dir, ".git/hooks/pre-push"); write(path.dirname(hook), path.basename(hook), "#!/bin/sh\nexit 1\n"); fs.chmodSync(hook, 0o755);
    assert.match(go(a.dir, ["save", "-m", "checkpoint"], 1), /handoff incomplete/); const receipts = await operations.inspectIncompleteOperations(a.repo); assert.equal(receipts.length, 1);
    fs.unlinkSync(hook); await recovery.recoverIncompleteOperation(a.repo, receipts[0].plan.operationId); go(a.dir, ["save"]);
  }],
  ["TypeScript command lock blocks Go; Go can resume after release", async ({ clone }) => {
    const a = await clone("a"); await sync.initializeRepository(a.repo); const lock = await safety.acquireRepositoryCommandLock(a.repo, "TS holds lock");
    try { assert.match(go(a.dir, ["get"], 1), /COMMAND_IN_PROGRESS/); } finally { await lock.release(); } go(a.dir, ["get"]);
  }],
  ["Go Finish delete -> TypeScript Undo -> Go", async ({ clone }) => {
    const a = await clone("a"); go(a.dir, ["init"]); go(a.dir, ["start", "feature"]); write(a.dir, "work.txt", "work"); go(a.dir, ["save", "-m", "work"]);
    go(a.dir, ["finish", "--disposition", "delete", "--yes"]); await undo.undoLastAction(a.repo);
    assert.equal(await a.repo.currentBranch(), "feature"); assert.equal(await a.repo.getConfig("branch.feature.wipstreamParent"), "main"); go(a.dir, ["get"]);
  }],
  ["TypeScript checkpoint -> Go Undo restores exact bytes", async ({ clone }) => {
    const a = await clone("a"); await sync.initializeRepository(a.repo); write(a.dir, "binary.dat", Buffer.from([0, 1, 255])); write(a.dir, "spaces.txt", " spaces  \n\n"); fs.unlinkSync(path.join(a.dir, "main.txt"));
    await saved(a.repo); go(a.dir, ["undo", "--yes"]); assert.deepEqual(fs.readFileSync(path.join(a.dir, "binary.dat")), Buffer.from([0, 1, 255])); assert.equal(fs.readFileSync(path.join(a.dir, "spaces.txt"), "utf8"), " spaces  \n\n"); assert.equal(fs.existsSync(path.join(a.dir, "main.txt")), false);
  }],
];
(async () => { for (const [name, run] of cases) { await fixture(run); console.log("PASS " + name); } console.log(cases.length + " interoperability scenarios passed"); })().catch(e => { console.error(e.stack); process.exitCode = 1; });
