package workflow

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lewisl/wip-stream-go/internal/operations"
)

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

// Scenarios from commit-and-save.test.js; only the Go workflow is invoked.
func TestSavePublishesEverySafeLocalBranchAndImportsRemoteChanges(t *testing.T) {
	f := newFixture(t)
	start(t, f.first, "local-a")
	commitFile(t, f.first, "a.txt", "a\n")
	command(t, f.first.Root, "switch", "main")
	start(t, f.first, "local-b")
	savedWork(t, f.second, "remote.txt", "remote advance\n")
	write(t, f.first.Root, "b.txt", "b\n")
	calls := 0
	opts := options("checkpoint b")
	opts.Message = func(suggested string) (string, error) {
		calls++
		if !strings.HasPrefix(suggested, "WIP checkpoint ") {
			t.Fatal(suggested)
		}
		return "checkpoint b", nil
	}
	result, err := Save(f.first, opts)
	requireOK(t, result, err)
	if !result.CheckpointCreated || !result.Published || result.Checkout != "local-b" || calls != 1 {
		t.Fatal(result, calls)
	}
	assertParity(t, f)
	for _, branch := range []string{"main", "local-a", "local-b"} {
		if command(t, f.first.Root, "config", "--get", "branch."+branch+".remote") != "origin" {
			t.Fatal("missing tracking configuration for", branch)
		}
	}
	receipt := assertReceipt(t, f.first, result.OperationID, "completed")
	if receipt.Plan.Checkpoint == nil || receipt.Plan.Checkpoint.Message != "checkpoint b" {
		t.Fatal("checkpoint not recorded")
	}
	if command(t, f.first.Root, "rev-parse", operations.RecoveryRef(result.OperationID, len(receipt.Plan.LocalRefUpdates))) != receipt.Plan.Checkpoint.Before {
		t.Fatal("checkpoint recovery ref missing")
	}
	calls = 0
	result, err = Save(f.first, opts)
	requireOK(t, result, err)
	if result.CheckpointCreated || !result.Published || calls != 0 {
		t.Fatal("clean Save prompted or created a checkpoint", result, calls)
	}
}

func TestSaveOfflineRetainsCheckpoint(t *testing.T) {
	f := newFixture(t)
	before := command(t, f.remote, "rev-parse", "main")
	write(t, f.first.Root, "offline.txt", "safe locally\n")
	unavailable := filepath.Join(f.root, "offline.git")
	if err := os.Rename(f.remote, unavailable); err != nil {
		t.Fatal(err)
	}
	result, err := Save(f.first, options("offline checkpoint"))
	if err == nil || !result.CheckpointCreated || result.Published {
		t.Fatal(result, err)
	}
	requireMessage(t, result, "Do not resume")
	if command(t, unavailable, "rev-parse", "main") != before || command(t, f.first.Root, "show", "HEAD:offline.txt") != "safe locally" || command(t, f.first.Root, "status", "--porcelain") != "" {
		t.Fatal("offline checkpoint or remote incorrect")
	}
	assertNoIncomplete(t, f.first)
}

func TestSaveRejectedCommitHookKeepsStagedWork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX executable Git hook")
	}
	f := newFixture(t)
	before := command(t, f.first.Root, "rev-parse", "HEAD")
	common, err := f.first.CommonDir()
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(common, "hooks", "pre-commit")
	if err = os.WriteFile(hook, []byte("#!/bin/sh\necho blocked >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	write(t, f.first.Root, "blocked.txt", "stays staged\n")
	_, err = Save(f.first, options("rejected"))
	requireError(t, err, "blocked")
	if command(t, f.first.Root, "rev-parse", "HEAD") != before || command(t, f.remote, "rev-parse", "main") != before || command(t, f.first.Root, "diff", "--cached", "--name-only") != "blocked.txt" {
		t.Fatal("rejected commit changed history or lost staged work")
	}
	assertNoIncomplete(t, f.first)
}

func TestSaveRejectsChangesMadeDuringMessagePrompt(t *testing.T) {
	f := newFixture(t)
	before := command(t, f.first.Root, "rev-parse", "HEAD")
	write(t, f.first.Root, "main.txt", "before prompt\n")
	opts := options("")
	opts.Message = func(string) (string, error) {
		write(t, f.first.Root, "main.txt", "during prompt\n")
		return "checkpoint", nil
	}
	_, err := Save(f.first, opts)
	requireError(t, err, "WORKTREE_CHANGED_DURING_CHECKPOINT")
	if command(t, f.first.Root, "rev-parse", "HEAD") != before || command(t, f.remote, "rev-parse", "main") != before {
		t.Fatal("changed prompt state was committed")
	}
	content, err := os.ReadFile(filepath.Join(f.first.Root, "main.txt"))
	if err != nil || string(content) != "during prompt\n" {
		t.Fatal("new work lost", err)
	}
	assertNoIncomplete(t, f.first)
}
