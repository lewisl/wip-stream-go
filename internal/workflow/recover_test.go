package workflow

import (
	"reflect"
	"testing"

	"github.com/lewisl/wip-stream-go/internal/operations"
)

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

// Selection and refusal scenarios from recovery-workflow.test.js.
func TestRecoverSelectionAndRefusals(t *testing.T) {
	f := newFixture(t)
	unchangedRefusal(t, f, "NO_INCOMPLETE_OPERATION", func() (Result, error) { return Recover(f.first, options("")) })
	pending := []*operations.Receipt{}
	for _, name := range []string{"Get from Remote", "Commit and Save"} {
		p := operations.NewPlan(name)
		p.Checkout = operations.Checkout{Before: "main", After: "main"}
		receipt, err := operations.Begin(f.first, p)
		if err != nil {
			t.Fatal(err)
		}
		if err = operations.Phase(f.first, receipt, "before-remote-fetch"); err != nil {
			t.Fatal(err)
		}
		pending = append(pending, receipt)
	}
	unchangedRefusal(t, f, "OPERATION_SELECTION_REQUIRED", func() (Result, error) { return Recover(f.first, options("")) })
	opts := options("")
	opts.OperationID = pending[0].Plan.OperationID
	opts.Confirm = func(string) (bool, error) { return false, nil }
	unchangedRefusal(t, f, "CANCELLED", func() (Result, error) { return Recover(f.first, opts) })
	write(t, f.first.Root, "main.txt", "staged\n")
	command(t, f.first.Root, "add", "main.txt")
	write(t, f.first.Root, "main.txt", "unstaged\n")
	write(t, f.first.Root, "untracked.txt", "untracked\n")
	before := observe(t, f)
	opts.Confirm = func(string) (bool, error) { return true, nil }
	result, err := Recover(f.first, opts)
	requireOK(t, result, err)
	if result.Published {
		t.Fatal("Recover claimed handoff")
	}
	assertReceipt(t, f.first, pending[0].Plan.OperationID, "recovered")
	assertReceipt(t, f.first, pending[1].Plan.OperationID, "in-progress")
	after := observe(t, f)
	before.Receipts = after.Receipts
	if !reflect.DeepEqual(before, after) {
		t.Fatal("Recover changed files, index, config, or refs")
	}
	unchangedRefusal(t, f, "INCOMPLETE_WIPSTREAM_OPERATION", func() (Result, error) { return Save(f.first, options("blocked")) })
}

func TestRecoverRefusesActiveMerge(t *testing.T) {
	f := conflictingFixture(t)
	result, err := Update(f.first, Options{})
	requireOK(t, result, err)
	unchangedRefusal(t, f, "GIT_OPERATION_IN_PROGRESS", func() (Result, error) { return Recover(f.first, options("")) })
}
