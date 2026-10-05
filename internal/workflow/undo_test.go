package workflow

import (
	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
	"os"
	"path/filepath"
	"testing"
)

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

// Scenarios from undo-workflow.test.js: exact restoration and later work guards.
func TestUndoGetRemovesImportedBranchAndTrackingConfiguration(t *testing.T) {
	f := newFixture(t)
	start(t, f.second, "new-topic")
	savedWork(t, f.second, "topic.txt", "topic\n")
	command(t, f.second.Root, "switch", "main")
	savedWork(t, f.second, "remote.txt", "remote\n")
	before := command(t, f.first.Root, "rev-parse", "main")
	fetched := get(t, f.first)
	result, err := Undo(f.first, options(""))
	requireOK(t, result, err)
	if result.Checkout != "main" || command(t, f.first.Root, "rev-parse", "main") != before {
		t.Fatal("Undo did not restore main")
	}
	assertAbsent(t, f.first, git.Local("new-topic"))
	keys, err := f.first.BranchConfigKeys("new-topic")
	if err != nil || len(keys) != 0 {
		t.Fatal("imported tracking config not removed", keys, err)
	}
	assertReceipt(t, f.first, fetched.OperationID, "undone")
	if command(t, f.remote, "show", "new-topic:topic.txt") != "topic" {
		t.Fatal("Undo of Get changed remote history")
	}
}

func TestUndoRefusesLaterWorkAndChangedCheckout(t *testing.T) {
	for _, condition := range []string{"local-commit", "working-file", "remote-commit", "changed-checkout", "changed-config", "declined"} {
		t.Run(condition, func(t *testing.T) {
			f := newFixture(t)
			start(t, f.first, "feature")
			savedWork(t, f.first, "feature.txt", "feature\n")
			opts := options("")
			code := "UNDO_NOT_ELIGIBLE"
			switch condition {
			case "local-commit":
				commitFile(t, f.first, "later.txt", "later\n")
			case "working-file":
				write(t, f.first.Root, "later.txt", "later\n")
			case "remote-commit":
				get(t, f.second)
				command(t, f.second.Root, "switch", "feature")
				savedWork(t, f.second, "later.txt", "remote later\n")
				code = "REMOTE_CHANGED_AFTER_OPERATION"
			case "changed-checkout":
				command(t, f.first.Root, "switch", "main")
			case "changed-config":
				command(t, f.first.Root, "config", "branch.feature.remote", "changed")
			case "declined":
				opts.Confirm = func(string) (bool, error) { return false, nil }
				code = "CANCELLED"
			}
			unchangedRefusal(t, f, code, func() (Result, error) { return Undo(f.first, opts) })
		})
	}
}

func TestUndoRefusesAfterRecovery(t *testing.T) {
	f := newFixture(t)
	p := operations.NewPlan("Get from Remote")
	p.Checkout = operations.Checkout{Before: "main", After: "main"}
	receipt, err := operations.Begin(f.first, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = operations.Phase(f.first, receipt, "before-remote-fetch"); err != nil {
		t.Fatal(err)
	}
	result, err := Recover(f.first, options(""))
	requireOK(t, result, err)
	unchangedRefusal(t, f, "UNDO_NOT_ELIGIBLE", func() (Result, error) { return Undo(f.first, options("")) })
}
