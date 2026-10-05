package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
)

type fixture struct {
	root, remote  string
	first, second *git.Repository
}

func command(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}
func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "fixture-marker"), []byte("disposable"), 0600); e != nil {
		t.Fatal(e)
	}
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	command(t, root, "init", "--bare", "--initial-branch=main", remote)
	command(t, root, "init", "--initial-branch=main", seed)
	command(t, seed, "config", "user.name", "Fixture")
	command(t, seed, "config", "user.email", "fixture@example.invalid")
	write(t, seed, "main.txt", "base\n")
	command(t, seed, "add", ".")
	command(t, seed, "commit", "-m", "base")
	command(t, seed, "remote", "add", "origin", remote)
	command(t, seed, "push", "-u", "origin", "main")
	repos := []*git.Repository{}
	for _, name := range []string{"first", "second"} {
		dir := filepath.Join(root, name)
		command(t, root, "clone", remote, dir)
		command(t, dir, "config", "user.name", "Fixture")
		command(t, dir, "config", "user.email", "fixture@example.invalid")
		r, e := git.Open(context.Background(), dir)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = Init(r, Options{}); e != nil {
			t.Fatal(e)
		}
		repos = append(repos, r)
	}
	return fixture{root, remote, repos[0], repos[1]}
}
func write(t *testing.T, root, name, text string) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
}
func options(message string) Options {
	return Options{Message: func(string) (string, error) { return message, nil }, Confirm: func(string) (bool, error) { return true, nil }}
}
func requireOK(t *testing.T, r Result, e error) Result {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func requireError(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || !strings.Contains(e.Error(), code) {
		t.Fatalf("expected %s, got %v", code, e)
	}
}
func TestSaveGetAndBranchDeletion(t *testing.T) {
	f := newFixture(t)
	r, e := Start(f.first, "feature")
	requireOK(t, r, e)
	write(t, f.first.Root, "work.txt", "checkpoint\n")
	r, e = Save(f.first, options("work"))
	requireOK(t, r, e)
	r, e = Get(f.second)
	requireOK(t, r, e)
	command(t, f.second.Root, "switch", "feature")
	r, e = Get(f.second)
	requireOK(t, r, e)
	if r.Checkout != "feature" {
		t.Fatal(r)
	}
	opts := options("finish")
	opts.Disposition = "delete"
	r, e = Finish(f.first, opts)
	requireOK(t, r, e)
	r, e = Get(f.second)
	requireOK(t, r, e)
	if r.Checkout != "main" {
		t.Fatal(r)
	}
	if command(t, f.second.Root, "show", "HEAD:work.txt") != "checkpoint" {
		t.Fatal("missing work")
	}
	if _, e = f.second.Object(git.Local("feature")); e != nil {
		t.Fatal(e)
	}
}
func TestSaveDivergenceRetainsCheckpoint(t *testing.T) {
	f := newFixture(t)
	write(t, f.second.Root, "remote.txt", "remote\n")
	r, e := Save(f.second, options("remote"))
	requireOK(t, r, e)
	write(t, f.first.Root, "local.txt", "local\n")
	r, e = Save(f.first, options("local"))
	requireError(t, e, "UNSAFE_BRANCHES")
	if r.Published {
		t.Fatal("claimed publication")
	}
	if command(t, f.first.Root, "show", "HEAD:local.txt") != "local" {
		t.Fatal("checkpoint lost")
	}
	if command(t, f.remote, "rev-parse", "refs/heads/main") != command(t, f.second.Root, "rev-parse", "HEAD") {
		t.Fatal("remote mutated")
	}
	r, e = Reconcile(f.first, options("reconcile"))
	requireOK(t, r, e)
	if !r.Published {
		t.Fatal("reconcile not saved")
	}
}
func TestGetRefusesUnpublishedAndDirtyWork(t *testing.T) {
	f := newFixture(t)
	write(t, f.first.Root, "dirty.txt", "dirty\n")
	_, e := Get(f.first)
	requireError(t, e, "DIRTY_WORKTREE")
	command(t, f.first.Root, "add", ".")
	command(t, f.first.Root, "commit", "-m", "unpublished")
	before := command(t, f.first.Root, "rev-parse", "HEAD")
	_, e = Get(f.first)
	requireError(t, e, "UNSAFE_BRANCHES")
	if command(t, f.first.Root, "rev-parse", "HEAD") != before {
		t.Fatal("get moved branch")
	}
}
func conflictingFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	r, e := Start(f.first, "feature")
	requireOK(t, r, e)
	write(t, f.first.Root, "main.txt", "feature\n")
	r, e = Save(f.first, options("feature"))
	requireOK(t, r, e)
	write(t, f.second.Root, "main.txt", "parent\n")
	r, e = Save(f.second, options("parent"))
	requireOK(t, r, e)
	return f
}
func TestPendingMergeAbort(t *testing.T) {
	f := conflictingFixture(t)
	before := command(t, f.first.Root, "rev-parse", "HEAD")
	r, e := Update(f.first, Options{})
	requireOK(t, r, e)
	if !r.Pending || !slices.Contains(r.Conflicts, "main.txt") {
		t.Fatal(r)
	}
	_, e = Save(f.first, options("blocked"))
	requireError(t, e, "GIT_OPERATION_IN_PROGRESS")
	r, e = Abort(f.first)
	requireOK(t, r, e)
	if command(t, f.first.Root, "rev-parse", "HEAD") != before || command(t, f.first.Root, "status", "--porcelain") != "" {
		t.Fatal("abort did not restore state")
	}
}
func TestPendingMergeContinue(t *testing.T) {
	f := conflictingFixture(t)
	r, e := Update(f.first, Options{})
	requireOK(t, r, e)
	if !r.Pending {
		t.Fatal(r)
	}
	write(t, f.first.Root, "main.txt", "resolved\n")
	command(t, f.first.Root, "add", "main.txt")
	r, e = Continue(f.first, options("resolved"))
	requireOK(t, r, e)
	if !r.Published {
		t.Fatal(r)
	}
	if command(t, f.first.Root, "show", "HEAD:main.txt") != "resolved" {
		t.Fatal("wrong merged contents")
	}
}
func TestCondenseAndUndo(t *testing.T) {
	f := newFixture(t)
	r, e := Start(f.first, "feature")
	requireOK(t, r, e)
	for _, value := range []string{"one", "two"} {
		write(t, f.first.Root, "work.txt", value)
		r, e = Save(f.first, options(value))
		requireOK(t, r, e)
	}
	before := command(t, f.first.Root, "rev-parse", "HEAD")
	r, e = Condense(f.first, options("condensed"))
	requireOK(t, r, e)
	if command(t, f.first.Root, "rev-list", "--count", "main..feature") != "1" {
		t.Fatal("not condensed")
	}
	r, e = Undo(f.first, options(""))
	requireOK(t, r, e)
	if command(t, f.first.Root, "rev-parse", "HEAD") != before || command(t, f.remote, "rev-parse", "feature") != before {
		t.Fatal("undo did not restore refs")
	}
}
func TestUndoRestoresBinaryDeletedAndWhitespaceFiles(t *testing.T) {
	f := newFixture(t)
	write(t, f.first.Root, "binary.dat", string([]byte{0, 1, 2, 255}))
	write(t, f.first.Root, "spaces.txt", " spaces  \n\n")
	if e := os.Remove(filepath.Join(f.first.Root, "main.txt")); e != nil {
		t.Fatal(e)
	}
	r, e := Save(f.first, options("checkpoint"))
	requireOK(t, r, e)
	r, e = Undo(f.first, options(""))
	requireOK(t, r, e)
	b, e := os.ReadFile(filepath.Join(f.first.Root, "binary.dat"))
	if e != nil || string(b) != string([]byte{0, 1, 2, 255}) {
		t.Fatal("binary restoration failed", e)
	}
	b, e = os.ReadFile(filepath.Join(f.first.Root, "spaces.txt"))
	if e != nil || string(b) != " spaces  \n\n" {
		t.Fatal("whitespace changed", e)
	}
	if _, e = os.Stat(filepath.Join(f.first.Root, "main.txt")); !os.IsNotExist(e) {
		t.Fatal("deleted file restored")
	}
}
func TestRemoteAdoptionPreservesIgnoredAndBacksUp(t *testing.T) {
	f := newFixture(t)
	write(t, f.first.Root, ".gitignore", "cache/\n")
	command(t, f.first.Root, "add", ".")
	command(t, f.first.Root, "commit", "-m", "ignore")
	r, e := Save(f.first, options("ignore"))
	requireOK(t, r, e)
	if e = os.Mkdir(filepath.Join(f.first.Root, "cache"), 0700); e != nil {
		t.Fatal(e)
	}
	write(t, f.first.Root, "cache/ignored.txt", "keep")
	write(t, f.first.Root, "main.txt", "discarded")
	write(t, f.first.Root, "untracked.txt", "remove")
	opts := Options{Authority: "remote", BackupParent: f.root}
	r, e = Init(f.first, opts)
	requireOK(t, r, e)
	b, e := os.ReadFile(filepath.Join(f.first.Root, "cache/ignored.txt"))
	if e != nil || string(b) != "keep" {
		t.Fatal("ignored work lost")
	}
	if _, e = os.Stat(filepath.Join(f.first.Root, "untracked.txt")); !os.IsNotExist(e) {
		t.Fatal("untracked entry not removed")
	}
	if !strings.Contains(r.Message, "verified backup") {
		t.Fatal(r)
	}
	_, e = Undo(f.first, options(""))
	requireError(t, e, "UNDO_NOT_ELIGIBLE")
}
func TestStaleApprovalRefusesChangedFiles(t *testing.T) {
	f := newFixture(t)
	write(t, f.first.Root, "main.txt", "before")
	opts := Options{InitChoice: func(string) (string, error) { write(t, f.first.Root, "main.txt", "after"); return "remote", nil }, Discard: true}
	_, e := Init(f.first, opts)
	requireError(t, e, "SETUP_STATE_CHANGED")
	b, e := os.ReadFile(filepath.Join(f.first.Root, "main.txt"))
	if e != nil || string(b) != "after" {
		t.Fatal("work discarded")
	}
}
func TestMalformedReceiptBlocksMutation(t *testing.T) {
	f := newFixture(t)
	common, e := f.first.CommonDir()
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(common, "wipstream", "operations", "corrupt.json")
	// Intentionally malformed fixture, never a receipt in a real project.
	if e = os.WriteFile(p, []byte("{}"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e = Save(f.first, options(""))
	requireError(t, e, "INVALID_OPERATION_RECEIPT")
}
func TestInteroperableLockRecordBlocksOtherCommands(t *testing.T) {
	f := newFixture(t)
	release, e := acquire(f.first, "TypeScript-shaped lock")
	if e != nil {
		t.Fatal(e)
	}
	_, e = Get(f.first)
	requireError(t, e, "COMMAND_IN_PROGRESS")
	if e = release(); e != nil {
		t.Fatal(e)
	}
}
func TestRecoveryKeepsDirtyFilesAndRefs(t *testing.T) {
	f := newFixture(t)
	p := operations.NewPlan("Get from Remote")
	p.Checkout = operations.Checkout{Before: "main", After: "main"}
	receipt, e := operations.Begin(f.first, p)
	if e != nil {
		t.Fatal(e)
	}
	if e = operations.Phase(f.first, receipt, "before-remote-fetch"); e != nil {
		t.Fatal(e)
	}
	write(t, f.first.Root, "main.txt", "staged\n")
	command(t, f.first.Root, "add", ".")
	write(t, f.first.Root, "main.txt", "unstaged\n")
	write(t, f.first.Root, "untracked.txt", "untracked\n")
	before := command(t, f.first.Root, "status", "--porcelain")
	head := command(t, f.first.Root, "rev-parse", "HEAD")
	r, e := Recover(f.first, options(""))
	requireOK(t, r, e)
	if command(t, f.first.Root, "status", "--porcelain") != before || command(t, f.first.Root, "rev-parse", "HEAD") != head {
		t.Fatal("recovery changed work")
	}
}
