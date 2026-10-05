// Install the repository VSIX and launch a disposable, real VS Code test host.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const code = process.env.WIPSTREAM_CODE || "code";
const vsix = process.env.WIPSTREAM_VSIX;
if (!vsix || !fs.existsSync(vsix)) throw Error("Set WIPSTREAM_VSIX to the VSIX from lewisl/wip-stream");
const binary = path.resolve(process.env.WIPSTREAM_GO_BINARY || "bin/wipstream");
assert.ok(fs.existsSync(binary), "Build bin/wipstream first");
const root = fs.mkdtempSync(path.join(os.tmpdir(), "wipstream-vscode-roundtrip-"));
const noSandbox = process.env.WIPSTREAM_CODE_NO_SANDBOX === "1" ? ["--no-sandbox"] : [];
function run(executable, args, cwd = root, timeout = 15000) {
  const r = spawnSync(executable, args, { cwd, encoding: "utf8", timeout, env: { ...process.env, WIPSTREAM_GO_BINARY: binary } });
  assert.equal(r.status, 0, r.stderr || r.stdout || r.error); return r;
}
try {
  fs.writeFileSync(path.join(root, "fixture-marker"), "disposable");
  const remote = path.join(root, "remote.git"), seed = path.join(root, "seed"), workspace = path.join(root, "clone");
  run("git", ["init", "--bare", "--initial-branch=main", remote]);
  run("git", ["init", "--initial-branch=main", seed]);
  run("git", ["config", "user.name", "Fixture"], seed); run("git", ["config", "user.email", "fixture@example.invalid"], seed);
  fs.writeFileSync(path.join(seed, "main.txt"), "base\n");
  run("git", ["add", "."], seed); run("git", ["commit", "-m", "base"], seed);
  run("git", ["remote", "add", "origin", remote], seed); run("git", ["push", "origin", "main"], seed);
  run("git", ["clone", remote, workspace]);
  run("git", ["config", "user.name", "Fixture"], workspace); run("git", ["config", "user.email", "fixture@example.invalid"], workspace);
  const driver = path.join(root, "driver"), profile = path.join(root, "profile"), extensions = path.join(root, "extensions");
  fs.mkdirSync(driver);
  fs.writeFileSync(path.join(driver, "package.json"), JSON.stringify({ name: "wipstream-roundtrip-driver", publisher: "local", version: "0.0.1", engines: { vscode: "^1.90.0" }, main: "./driver.js", activationEvents: [] }));
  fs.copyFileSync(path.join(__dirname, "vscode-roundtrip-host.js"), path.join(driver, "driver.js"));
  run(code, [...noSandbox, "--user-data-dir", profile, "--extensions-dir", extensions, "--install-extension", path.resolve(vsix), "--force"], root, 30000);
  const launched = spawnSync(code, [
    ...noSandbox, "--new-window", "--wait", "--disable-workspace-trust", "--skip-welcome", "--skip-release-notes",
    "--user-data-dir", profile, "--extensions-dir", extensions,
    "--extensionDevelopmentPath", driver, "--extensionTestsPath", path.join(driver, "driver.js"), workspace,
  ], { cwd: root, encoding: "utf8", timeout: 120000, env: { ...process.env, WIPSTREAM_GO_BINARY: binary } });
  fs.mkdirSync("test-results", { recursive: true });
  fs.writeFileSync("test-results/vscode-host.log", launched.stdout + launched.stderr);
  const resultPath = path.join(root, "roundtrip-result.json");
  assert.ok(fs.existsSync(resultPath), "Host produced no result: " + (launched.error || launched.stderr || launched.stdout));
  const result = JSON.parse(fs.readFileSync(resultPath, "utf8"));
  fs.writeFileSync("test-results/vscode-roundtrip.json", JSON.stringify(result, null, 2) + "\n");
  assert.equal(result.passed, true, result.error);
  assert.equal(launched.status, 0, launched.error || launched.stderr);
  console.log(JSON.stringify(result, null, 2));
} finally {
  if (process.env.WIPSTREAM_KEEP_FIXTURE === "1") console.log("Fixture retained at " + root);
  else fs.rmSync(root, { recursive: true, force: true });
}
