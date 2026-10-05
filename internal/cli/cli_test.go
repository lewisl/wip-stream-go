package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
	"github.com/lewisl/wip-stream-go/internal/workflow"
)

// These tests invoke the real Cobra handlers against disposable Git fixtures.
// The extension's command-surface and command-handlers suites supply scenarios;
// no extension, JavaScript runtime, terminal, or workflow mocks are involved.
func TestCommandSurface(t *testing.T) {
	expected := map[string][]string{
		"init": {"remote", "authority", "backup-parent", "discard-local-work", "message"},
		"get":  {}, "save": {"message"}, "start": {},
		"finish": {"disposition", "message", "parent", "yes"}, "update": {"parent"},
		"reconcile": {"message"}, "continue": {"message"}, "abort": {},
		"recover": {"operation", "yes"}, "undo": {"yes"}, "condense": {"message", "parent", "yes"},
	}
	root := New()
	if len(root.Commands()) != len(expected) {
		t.Fatalf("registered commands: %d, want %d", len(root.Commands()), len(expected))
	}
	for name, flags := range expected {
		t.Run(name, func(t *testing.T) {
			cmd, remaining, err := root.Find([]string{name})
			if err != nil || len(remaining) != 0 || cmd.Name() != name || cmd.RunE == nil {
				t.Fatal("command unavailable", name, err)
			}
			if cmd.Short == "" {
				t.Fatal("command lacks help")
			}
			for _, flag := range flags {
				if cmd.Flags().Lookup(flag) == nil {
					t.Fatalf("%s lacks --%s", name, flag)
				}
			}
			if err = cmd.Args(cmd, nil); err != nil {
				t.Fatal(err)
			}
			if name == "start" {
				if err = cmd.Args(cmd, []string{"feature"}); err != nil {
					t.Fatal(err)
				}
			} else if err = cmd.Args(cmd, []string{"unexpected"}); err == nil {
				t.Fatal("unexpected argument accepted")
			}
		})
	}
	for _, name := range []string{"repo", "json"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Fatal("missing global flag", name)
		}
	}
}

func TestCLIRejectsUnexpectedArgumentsBeforeOpeningRepository(t *testing.T) {
	for _, name := range []string{"init", "get", "save", "start", "finish", "update", "reconcile", "continue", "abort", "recover", "undo", "condense"} {
		t.Run(name, func(t *testing.T) {
			args := []string{name, "unexpected"}
			if name == "start" {
				args = append(args, "extra")
			}
			_, _, err := runCLI(t, filepath.Join(t.TempDir(), "missing"), args...)
			if err == nil || strings.Contains(err.Error(), "git ") {
				t.Fatalf("argument validation did not precede Git: %v", err)
			}
		})
	}
}

func TestCLIHelpDoesNotRequireRepository(t *testing.T) {
	root := New()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"-C", filepath.Join(t.TempDir(), "missing"), "save", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--message") {
		t.Fatal("help lacks message flag")
	}
}

func TestCLILifecycle(t *testing.T) {
	for _, disposition := range []string{"retain", "delete"} {
		t.Run(disposition, func(t *testing.T) {
			f := cliFixture(t)
			execute(t, f.first, "init")
			execute(t, f.first, "get")
			execute(t, f.first, "start", "feature")
			cliWrite(t, f.first, "work.txt", "one\n")
			saved := execute(t, f.first, "save", "-m", "first checkpoint")
			if !saved.CheckpointCreated || !saved.Published {
				t.Fatal(saved)
			}
			noOp := execute(t, f.first, "save")
			if noOp.CheckpointCreated || !noOp.Published {
				t.Fatal("clean CLI Save requested input", noOp)
			}
			cliWrite(t, f.first, "work.txt", "two\n")
			execute(t, f.first, "save", "--message", "second checkpoint")
			before := cliGit(t, f.first, "rev-parse", "HEAD")
			execute(t, f.first, "condense", "-m", "final", "--yes")
			if cliGit(t, f.first, "rev-list", "--count", "main..feature") != "1" {
				t.Fatal("CLI Condense not dispatched")
			}
			execute(t, f.first, "undo", "-y")
			if cliGit(t, f.first, "rev-parse", "HEAD") != before {
				t.Fatal("CLI Undo not dispatched")
			}
			finished := execute(t, f.first, "finish", "--disposition", disposition, "--yes")
			if !finished.Published || finished.Checkout != "main" || cliGit(t, f.first, "show", "HEAD:work.txt") != "two" {
				t.Fatal("CLI Finish did not integrate work", finished)
			}
			heads := cliGit(t, f.first, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")
			if heads != cliGit(t, f.remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/") {
				t.Fatal("CLI lifecycle ended without branch parity")
			}
			present := strings.Contains(heads, "refs/heads/feature ")
			if present != (disposition == "retain") {
				t.Fatal("disposition not honored", heads)
			}
		})
	}
}

func TestCLIUpdateAbortAndContinue(t *testing.T) {
	f := cliFixture(t)
	execute(t, f.first, "init")
	execute(t, f.second, "init")
	execute(t, f.first, "start", "feature")
	cliWrite(t, f.first, "main.txt", "feature\n")
	execute(t, f.first, "save", "-m", "feature")
	cliWrite(t, f.second, "main.txt", "parent\n")
	execute(t, f.second, "save", "-m", "parent")
	before := cliGit(t, f.first, "rev-parse", "HEAD")
	pending := execute(t, f.first, "update")
	if !pending.Pending || !slices.Contains(pending.Conflicts, "main.txt") {
		t.Fatal("CLI did not report pending merge", pending)
	}
	out, _, err := runCLI(t, f.first, "continue")
	if err == nil || !strings.Contains(err.Error(), "UNRESOLVED_CONFLICTS") || out != "" {
		t.Fatal("CLI unresolved Continue result", out, err)
	}
	execute(t, f.first, "abort")
	if cliGit(t, f.first, "rev-parse", "HEAD") != before || cliGit(t, f.first, "status", "--porcelain") != "" {
		t.Fatal("CLI Abort did not restore checkout")
	}
	pending = execute(t, f.first, "update")
	if !pending.Pending {
		t.Fatal(pending)
	}
	cliWrite(t, f.first, "main.txt", "resolved\n")
	cliGit(t, f.first, "add", "main.txt")
	result := execute(t, f.first, "continue")
	if !result.Published || cliGit(t, f.remote, "show", "feature:main.txt") != "resolved" {
		t.Fatal("CLI Continue did not publish resolution", result)
	}
}

func TestCLIReconcile(t *testing.T) {
	f := cliFixture(t)
	execute(t, f.first, "init")
	execute(t, f.second, "init")
	cliWrite(t, f.first, "local.txt", "local\n")
	cliGit(t, f.first, "add", ".")
	cliGit(t, f.first, "commit", "-m", "local")
	cliWrite(t, f.second, "remote.txt", "remote\n")
	execute(t, f.second, "save", "-m", "remote")
	result := execute(t, f.first, "reconcile")
	if !result.Published || cliGit(t, f.remote, "show", "main:local.txt") != "local" || cliGit(t, f.remote, "show", "main:remote.txt") != "remote" {
		t.Fatal("CLI Reconcile did not preserve both histories", result)
	}
}

func TestCLIRecoverKeepsWork(t *testing.T) {
	f := cliFixture(t)
	execute(t, f.first, "init")
	repo, err := git.Open(context.Background(), f.first)
	if err != nil {
		t.Fatal(err)
	}
	plan := operations.NewPlan("Get from Remote")
	plan.Checkout = operations.Checkout{Before: "main", After: "main"}
	receipt, err := operations.Begin(repo, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = operations.Phase(repo, receipt, "before-remote-fetch"); err != nil {
		t.Fatal(err)
	}
	cliWrite(t, f.first, "main.txt", "staged\n")
	cliGit(t, f.first, "add", "main.txt")
	cliWrite(t, f.first, "main.txt", "unstaged\n")
	cliWrite(t, f.first, "draft.txt", "draft\n")
	before := cliGit(t, f.first, "status", "--porcelain")
	head := cliGit(t, f.first, "rev-parse", "HEAD")
	result := execute(t, f.first, "recover", "--operation", plan.OperationID, "--yes")
	if result.Published || result.OperationID != plan.OperationID || cliGit(t, f.first, "status", "--porcelain") != before || cliGit(t, f.first, "rev-parse", "HEAD") != head {
		t.Fatal("CLI Recover changed work or claimed publication", result)
	}
	recovered, err := operations.Read(repo, plan.OperationID)
	if err != nil || recovered.Status != "recovered" {
		t.Fatal("selected receipt not recovered", err)
	}
}

func TestCLINoninteractiveRefusals(t *testing.T) {
	for _, condition := range []string{"start-name", "save-message", "save-blank-message", "finish-disposition", "finish-confirmation", "undo-confirmation", "init-authority"} {
		t.Run(condition, func(t *testing.T) {
			f := cliFixture(t)
			execute(t, f.first, "init")
			args := []string{}
			expected := "requires input"
			switch condition {
			case "start-name":
				args = []string{"start"}
			case "save-message":
				cliWrite(t, f.first, "draft.txt", "draft\n")
				args = []string{"save"}
			case "save-blank-message":
				cliWrite(t, f.first, "draft.txt", "draft\n")
				args = []string{"save", "-m", " \t "}
				expected = "cannot be blank"
			case "finish-disposition":
				args = []string{"finish"}
				expected = "--disposition"
			case "finish-confirmation":
				execute(t, f.first, "start", "feature")
				args = []string{"finish", "--disposition", "delete"}
				expected = "confirmation required"
			case "undo-confirmation":
				execute(t, f.first, "save")
				args = []string{"undo"}
				expected = "confirmation required"
			case "init-authority":
				cliWrite(t, f.first, "draft.txt", "draft\n")
				args = []string{"init"}
				expected = "authority choice required"
			}
			before := cliGit(t, f.first, "rev-parse", "HEAD")
			out, _, err := runCLI(t, f.first, args...)
			if err == nil || !strings.Contains(err.Error(), expected) || out != "" {
				t.Fatal("missing input should fail without success output", out, err)
			}
			if cliGit(t, f.first, "rev-parse", "HEAD") != before || cliGit(t, f.remote, "rev-parse", "main") != before {
				t.Fatal("missing input changed history")
			}
		})
	}
}

func TestCLIFailedSaveReportsLocalCheckpoint(t *testing.T) {
	f := cliFixture(t)
	execute(t, f.first, "init")
	execute(t, f.second, "init")
	cliWrite(t, f.second, "remote.txt", "remote\n")
	execute(t, f.second, "save", "-m", "remote")
	cliWrite(t, f.first, "local.txt", "local\n")
	out, diagnostic, err := runCLI(t, f.first, "save", "-m", "local checkpoint")
	if err == nil || out != "" || !strings.Contains(diagnostic, "Do not resume") || cliGit(t, f.first, "show", "HEAD:local.txt") != "local" {
		t.Fatal("failed Save diagnostic or checkpoint incorrect", out, diagnostic, err)
	}
}

type fixture struct{ root, remote, first, second string }

func cliFixture(t *testing.T) fixture {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	// Force a closed pipe so missing-input tests are deterministic even when
	// go test was launched from a terminal. These tests do not run in parallel.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
	previous := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = previous; reader.Close() })
	root := t.TempDir()
	cliWrite(t, root, "fixture-marker", "disposable")
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	cliGit(t, root, "init", "--bare", "--initial-branch=main", remote)
	cliGit(t, root, "init", "--initial-branch=main", seed)
	cliGit(t, seed, "config", "user.name", "Fixture")
	cliGit(t, seed, "config", "user.email", "fixture@example.invalid")
	cliWrite(t, seed, "main.txt", "base\n")
	cliGit(t, seed, "add", ".")
	cliGit(t, seed, "commit", "-m", "base")
	cliGit(t, seed, "remote", "add", "origin", remote)
	cliGit(t, seed, "push", "origin", "main")
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, dir := range []string{first, second} {
		cliGit(t, root, "clone", remote, dir)
		cliGit(t, dir, "config", "user.name", "Fixture")
		cliGit(t, dir, "config", "user.email", "fixture@example.invalid")
	}
	return fixture{root, remote, first, second}
}

func cliGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for p := resolved; ; p = filepath.Dir(p) {
		b, err := os.ReadFile(filepath.Join(p, "fixture-marker"))
		if err == nil && string(b) == "disposable" {
			break
		}
		if filepath.Dir(p) == p {
			t.Fatalf("Git target outside disposable fixture: %s", dir)
		}
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func cliWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func runCLI(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	root := New()
	var out, diagnostic bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&diagnostic)
	root.SetArgs(append([]string{"-C", dir, "--json"}, args...))
	err := root.Execute()
	return out.String(), diagnostic.String(), err
}

func execute(t *testing.T, dir string, args ...string) workflow.Result {
	t.Helper()
	out, diagnostic, err := runCLI(t, dir, args...)
	if err != nil {
		t.Fatalf("wipstream %v: %v\n%s", args, err, diagnostic)
	}
	var result workflow.Result
	if err = json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid CLI JSON %q: %v", out, err)
	}
	return result
}
