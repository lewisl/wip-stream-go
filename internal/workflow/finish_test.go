package workflow

import (
	"github.com/lewisl/wip-stream-go/internal/git"
	"testing"
)

// Scenarios from lifecycle-workflow.test.js and undo-workflow.test.js.
func TestFinishRetainAndDeleteThenUndo(t *testing.T) {
	for _, disposition := range []string{"retain", "delete"} {
		t.Run(disposition, func(t *testing.T) {
			f := newFixture(t)
			root := f.first.Root
			parentBefore := command(t, root, "rev-parse", "main")
			start(t, f.first, "feature")
			savedWork(t, f.first, "feature.txt", "work\n")
			branchTip := command(t, root, "rev-parse", "feature")
			opts := options("")
			opts.Disposition = disposition
			result, err := Finish(f.first, opts)
			requireOK(t, result, err)
			if result.Checkout != "main" || !result.Published || command(t, root, "rev-parse", "main") != branchTip {
				t.Fatal("parent not advanced", result)
			}
			assertParity(t, f)
			assertReceipt(t, f.first, result.OperationID, "completed")
			if disposition == "delete" {
				assertAbsent(t, f.first, git.Local("feature"))
				keys, err := f.first.BranchConfigKeys("feature")
				if err != nil || len(keys) != 0 {
					t.Fatal("deleted branch configuration retained", keys, err)
				}
			} else if command(t, root, "rev-parse", "feature") != branchTip || command(t, root, "config", "--get", "branch.feature.wipstreamParent") != "main" {
				t.Fatal("retained branch changed")
			}
			undone, err := Undo(f.first, options(""))
			requireOK(t, undone, err)
			if undone.Checkout != "feature" || command(t, root, "rev-parse", "main") != parentBefore || command(t, root, "rev-parse", "feature") != branchTip || command(t, root, "config", "--get", "branch.feature.wipstreamParent") != "main" {
				t.Fatal("Undo did not restore Finish state", undone)
			}
			assertParity(t, f)
			assertReceipt(t, f.first, result.OperationID, "undone")
		})
	}
}

func TestFinishRefusesBeforeCheckpointOrConfirmation(t *testing.T) {
	for _, condition := range []string{"default-branch", "parent-advanced", "declined", "missing-disposition"} {
		t.Run(condition, func(t *testing.T) {
			f := newFixture(t)
			opts := options("would checkpoint")
			opts.Disposition = "delete"
			calls := 0
			opts.Confirm = func(string) (bool, error) { calls++; return false, nil }
			code := "CANCELLED"
			if condition == "default-branch" {
				code = "DEFAULT_BRANCH"
			} else {
				start(t, f.first, "feature")
				savedWork(t, f.first, "feature.txt", "work\n")
				if condition == "parent-advanced" {
					command(t, f.first.Root, "switch", "main")
					savedWork(t, f.first, "parent.txt", "new parent\n")
					command(t, f.first.Root, "switch", "feature")
					code = "PARENT_UPDATE_REQUIRED"
				}
				if condition == "missing-disposition" {
					opts.Disposition = ""
					code = "DISPOSITION_REQUIRED"
				}
			}
			write(t, f.first.Root, "draft.txt", "must remain untracked\n")
			unchangedRefusal(t, f, code, func() (Result, error) { return Finish(f.first, opts) })
			expected := 0
			if condition == "declined" {
				expected = 1
			}
			if calls != expected {
				t.Fatalf("confirmation calls: %d, want %d", calls, expected)
			}
		})
	}
}
