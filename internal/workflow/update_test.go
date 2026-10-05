package workflow

import (
	"strings"
	"testing"
)

// Scenarios from lifecycle-workflow.test.js: imported parents and clean merges.
func TestUpdateMergesParentWithoutPublishingThenUndo(t *testing.T) {
	f := newFixture(t)
	root := f.first.Root
	start(t, f.first, "feature")
	savedWork(t, f.first, "feature.txt", "feature\n")
	before := command(t, root, "rev-parse", "feature")
	savedWork(t, f.second, "parent.txt", "parent\n")
	result, err := Update(f.first, Options{})
	requireOK(t, result, err)
	if result.Pending || result.Published || result.Checkout != "feature" {
		t.Fatal(result)
	}
	parents := command(t, root, "rev-list", "--parents", "-n", "1", "HEAD")
	if len(strings.Fields(parents)) != 3 || command(t, root, "show", "HEAD:parent.txt") != "parent" || command(t, f.remote, "rev-parse", "feature") != before {
		t.Fatal("Update did not create a local merge", parents)
	}
	assertReceipt(t, f.first, result.OperationID, "completed")
	undone, err := Undo(f.first, options(""))
	requireOK(t, undone, err)
	if command(t, root, "rev-parse", "feature") != before {
		t.Fatal("Undo did not restore pre-Update feature")
	}
	assertReceipt(t, f.first, result.OperationID, "undone")
}

func TestUpdateImportedBranchRequiresAndRecordsParent(t *testing.T) {
	f := newFixture(t)
	command(t, f.first.Root, "switch", "-c", "imported")
	savedWork(t, f.first, "imported.txt", "imported\n")
	_, err := Update(f.first, Options{})
	requireError(t, err, "PARENT_CONFIRMATION_REQUIRED")
	opts := Options{Parent: func(suggested string) (string, error) {
		if suggested != "main" {
			t.Fatal(suggested)
		}
		return suggested, nil
	}}
	result, err := Update(f.first, opts)
	requireOK(t, result, err)
	requireMessage(t, result, "Already contains parent")
	if command(t, f.first.Root, "config", "--get", "branch.imported.wipstreamParent") != "main" {
		t.Fatal("confirmed parent not recorded")
	}
}

func TestUpdateRefusesDirtyWork(t *testing.T) {
	f := newFixture(t)
	start(t, f.first, "feature")
	savedWork(t, f.first, "feature.txt", "feature\n")
	write(t, f.first.Root, "dirty.txt", "dirty\n")
	unchangedRefusal(t, f, "DIRTY_WORKTREE", func() (Result, error) { return Update(f.first, Options{}) })
}
