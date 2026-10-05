// Loaded by VS Code's real extension test host. Only the native input acceptance
// is automated; WipStream's public commands, Git adapter, and workflows run as shipped.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
exports.run = async function () {
  const vscode = require("vscode");
  const workspace = vscode.workspace.workspaceFolders[0].uri.fsPath;
  const root = path.dirname(workspace);
  const resultPath = path.join(root, "roundtrip-result.json");
  const evidence = { passed: false, steps: [] };
  function git(args) {
    const r = spawnSync("git", args, { cwd: workspace, encoding: "utf8" });
    assert.equal(r.status, 0, r.stderr || r.error); return r.stdout.trim();
  }
  function go(args) {
    const r = spawnSync(process.env.WIPSTREAM_GO_BINARY, ["-C", workspace, "--json", ...args], { encoding: "utf8" });
    assert.equal(r.status, 0, r.stderr || r.stdout || r.error); return JSON.parse(r.stdout);
  }
  async function command(name, acceptDefault = false) {
    // Use VS Code's native input controller to accept the default remote,
    // branch name, or checkpoint message when the shipped adapter asks for it.
    const timer = acceptDefault ? setInterval(() => {
      void vscode.commands.executeCommand("workbench.action.acceptSelectedQuickOpenItem");
    }, 200) : undefined;
    try {
      await Promise.race([
        vscode.commands.executeCommand("wipstream." + name),
        new Promise((_, reject) => { const timeout = setTimeout(() => reject(Error("Command timed out: " + name)), 25000); timeout.unref(); }),
      ]);
      evidence.steps.push("VS Code: wipstream." + name);
    } finally { if (timer) clearInterval(timer); }
  }
  try {
    assert.equal(fs.readFileSync(path.join(root, "fixture-marker"), "utf8"), "disposable");
    const extension = vscode.extensions.getExtension("lewisl.wipstream");
    assert.ok(extension, "GitHub VSIX installed in this test profile");
    await extension.activate();
    assert.equal(extension.isActive, true);
    evidence.vscodeVersion = vscode.version;
    evidence.extensionVersion = extension.packageJSON.version;
    const commands = await vscode.commands.getCommands(true);
    for (const name of ["init", "resume", "saveup", "start", "finish", "update", "reconcile", "continue", "abort", "recover", "undo", "condense"]) {
      assert.ok(commands.includes("wipstream." + name), "registered public command " + name);
    }
    // Fail promptly if a public adapter catches a workflow error and would
    // otherwise wait for dismissal of an error notification.
    const adapter = require(path.join(extension.extensionPath, "out/commands"));
    const originalErrorHandler = adapter.handleCommandError;
    adapter.handleCommandError = async (_output, _title, error) => { throw error; };
    try {
      await command("init", true);
      assert.equal(git(["config", "--local", "--get", "wipstream.remote"]), "origin");
      await command("start", true);
      const feature = git(["branch", "--show-current"]);
      assert.equal(feature, "change", "native Start input accepted its default");
      assert.equal(git(["config", "--local", "--get", "branch.change.wipstreamParent"]), "main");
      fs.writeFileSync(path.join(workspace, "extension.txt"), "created in VS Code extension\n");
      await command("saveup", true);
      assert.equal(git(["show", "origin/change:extension.txt"]), "created in VS Code extension");

      go(["get"]); evidence.steps.push("Go: get");
      fs.writeFileSync(path.join(workspace, "go.txt"), "created by Go CLI\n");
      const saved = go(["save", "-m", "Go roundtrip checkpoint"]);
      assert.equal(saved.published, true); evidence.steps.push("Go: save");
      evidence.goSaveOperation = saved.operationId;

      await command("resume");
      assert.equal(git(["show", "HEAD:go.txt"]), "created by Go CLI");
      fs.appendFileSync(path.join(workspace, "extension.txt"), "continued after Go\n");
      await command("saveup", true);
      assert.equal(git(["show", "origin/change:extension.txt"]), "created in VS Code extension\ncontinued after Go");
      const finished = go(["finish", "--disposition", "retain", "--yes"]);
      assert.equal(finished.checkout, "main"); evidence.steps.push("Go: finish retain");
      await command("resume");
      assert.equal(git(["branch", "--show-current"]), "main");
      assert.equal(git(["show", "main:go.txt"]), "created by Go CLI");
      assert.equal(git(["status", "--porcelain"]), "");
      const { GitRepository } = require(path.join(extension.extensionPath, "out/git"));
      const { listOperationReceipts, inspectIncompleteOperations } = require(path.join(extension.extensionPath, "out/operations"));
      const repo = await GitRepository.open(workspace);
      const receipts = await listOperationReceipts(repo);
      assert.ok(receipts.some(r => r.plan.operationId === saved.operationId && r.status === "completed"), "extension reads completed Go receipt");
      assert.equal((await inspectIncompleteOperations(repo)).length, 0);
      evidence.receiptsReadByExtension = receipts.length;
      evidence.finalHead = git(["rev-parse", "HEAD"]);
      evidence.finalBranch = "main";
      evidence.passed = true;
    } finally { adapter.handleCommandError = originalErrorHandler; }
  } catch (error) {
    evidence.error = error.stack || String(error); throw error;
  } finally { fs.writeFileSync(resultPath, JSON.stringify(evidence, null, 2) + "\n"); }
};
