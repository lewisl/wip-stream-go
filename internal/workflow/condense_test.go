package workflow

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

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

// Scenarios from workflow-refusals.test.js and lifecycle-workflow.test.js.
func TestCondensePreservesTreeAndUsesParentAsOnlyParent(t *testing.T) {
	f := newFixture(t)
	start(t, f.first, "feature")
	savedWork(t, f.first, "one.txt", "one\n")
	savedWork(t, f.first, "two.txt", "two\n")
	tree := command(t, f.first.Root, "rev-parse", "HEAD^{tree}")
	parent := command(t, f.first.Root, "rev-parse", "main")
	result, err := Condense(f.first, options("one final commit"))
	requireOK(t, result, err)
	if command(t, f.first.Root, "rev-parse", "HEAD^{tree}") != tree || command(t, f.first.Root, "show", "-s", "--format=%P", "HEAD") != parent || command(t, f.first.Root, "show", "-s", "--format=%s", "HEAD") != "one final commit" {
		t.Fatal("Condense changed tree or ancestry")
	}
	assertParity(t, f)
	assertReceipt(t, f.first, result.OperationID, "completed")
}

func TestCondenseRefusals(t *testing.T) {
	for _, condition := range []string{"zero", "one", "declined", "message-blank", "message-cancelled", "parent-advanced", "dirty"} {
		t.Run(condition, func(t *testing.T) {
			f := newFixture(t)
			start(t, f.first, "feature")
			count := 2
			if condition == "zero" {
				count = 0
			}
			if condition == "one" {
				count = 1
			}
			for i := 0; i < count; i++ {
				savedWork(t, f.first, "work.txt", strconv.Itoa(i))
			}
			if count == 0 {
				result, err := Save(f.first, Options{})
				requireOK(t, result, err)
			}
			opts := options("condensed")
			code := "NOTHING_TO_CONDENSE"
			confirmCalls, messageCalls := 0, 0
			opts.Confirm = func(string) (bool, error) { confirmCalls++; return condition != "declined", nil }
			opts.Message = func(string) (string, error) {
				messageCalls++
				if condition == "message-blank" {
					return " \t ", nil
				}
				if condition == "message-cancelled" {
					return "", fmt.Errorf("CANCELLED")
				}
				return "condensed", nil
			}
			switch condition {
			case "declined", "message-cancelled":
				code = "CANCELLED"
			case "message-blank":
				code = "INVALID_CHECKPOINT_MESSAGE"
			case "parent-advanced":
				command(t, f.first.Root, "switch", "main")
				savedWork(t, f.first, "parent.txt", "parent\n")
				command(t, f.first.Root, "switch", "feature")
				code = "PARENT_UPDATE_REQUIRED"
			case "dirty":
				write(t, f.first.Root, "draft.txt", "draft\n")
				code = "DIRTY_WORKTREE"
			}
			unchangedRefusal(t, f, code, func() (Result, error) { return Condense(f.first, opts) })
			expectedConfirm, expectedMessage := 0, 0
			if condition == "declined" || strings.HasPrefix(condition, "message-") {
				expectedConfirm = 1
			}
			if strings.HasPrefix(condition, "message-") {
				expectedMessage = 1
			}
			if confirmCalls != expectedConfirm || messageCalls != expectedMessage {
				t.Fatal("unexpected prompt calls", confirmCalls, messageCalls)
			}
		})
	}
}

func TestCondenseRefusesChangesDuringConfirmation(t *testing.T) {
	f := newFixture(t)
	start(t, f.first, "feature")
	savedWork(t, f.first, "work.txt", "one\n")
	savedWork(t, f.first, "work.txt", "two\n")
	before := command(t, f.first.Root, "rev-parse", "HEAD")
	opts := options("condensed")
	opts.Confirm = func(string) (bool, error) {
		write(t, f.first.Root, "new.txt", "arrived during preview\n")
		return true, nil
	}
	_, err := Condense(f.first, opts)
	requireError(t, err, "LOCAL_STATE_CHANGED")
	if command(t, f.first.Root, "rev-parse", "HEAD") != before || command(t, f.remote, "rev-parse", "feature") != before {
		t.Fatal("stale approval rewrote history")
	}
	assertNoIncomplete(t, f.first)
}

func TestOtherCloneRetainsOldHistoryAfterCondense(t *testing.T) {
	f := newFixture(t)
	start(t, f.first, "feature")
	savedWork(t, f.first, "work.txt", "one\n")
	savedWork(t, f.first, "work.txt", "two\n")
	get(t, f.second)
	command(t, f.second.Root, "switch", "feature")
	oldTip := command(t, f.second.Root, "rev-parse", "feature")
	result, err := Condense(f.first, options("condensed"))
	requireOK(t, result, err)
	// Observe the second clone using the same independent refusal assertions.
	observer := fixture{root: f.root, remote: f.remote, first: f.second, second: f.first}
	unchangedRefusal(t, observer, "UNSAFE_BRANCHES", func() (Result, error) { return Get(f.second) })
	if command(t, f.second.Root, "rev-parse", "feature") != oldTip {
		t.Fatal("observer's old checkpoints lost")
	}
}
