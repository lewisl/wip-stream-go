package workflow

import (
	"slices"
	"testing"
)

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

// Continue and Abort guard scenarios from conflict-workflow.test.js.
func TestMergeCommandsRefuseWithoutPendingMerge(t *testing.T) {
	for _, action := range []string{"continue", "abort"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t)
			unchangedRefusal(t, f, "NO_PENDING_MERGE", func() (Result, error) {
				if action == "continue" {
					return Continue(f.first, Options{})
				}
				return Abort(f.first)
			})
		})
	}
}

func TestContinueRefusesUnresolvedConflicts(t *testing.T) {
	f := conflictingFixture(t)
	result, err := Update(f.first, Options{})
	requireOK(t, result, err)
	unchangedRefusal(t, f, "UNRESOLVED_CONFLICTS", func() (Result, error) { return Continue(f.first, Options{}) })
	assertReceipt(t, f.first, result.OperationID, "in-progress")
}

func TestMergeCommandsRefuseMismatchedMergeHead(t *testing.T) {
	for _, action := range []string{"continue", "abort"} {
		t.Run(action, func(t *testing.T) {
			f := conflictingFixture(t)
			result, err := Update(f.first, Options{})
			requireOK(t, result, err)
			write(t, f.first.Root, "main.txt", "resolved\n")
			command(t, f.first.Root, "add", "main.txt")
			common, err := f.first.CommonDir()
			if err != nil {
				t.Fatal(err)
			}
			// Replace only Git's disposable MERGE_HEAD, leaving the actual receipt intact.
			write(t, common, "MERGE_HEAD", command(t, f.first.Root, "rev-parse", "HEAD")+"\n")
			unchangedRefusal(t, f, "MERGE_STATE_MISMATCH", func() (Result, error) {
				if action == "continue" {
					return Continue(f.first, Options{})
				}
				return Abort(f.first)
			})
			assertReceipt(t, f.first, result.OperationID, "in-progress")
		})
	}
}

func TestExternalMergeCompletionAndAbortAreRecognized(t *testing.T) {
	for _, action := range []string{"complete", "abort"} {
		t.Run(action, func(t *testing.T) {
			f := conflictingFixture(t)
			result, err := Update(f.first, Options{})
			requireOK(t, result, err)
			resolution := "merge-aborted-externally"
			if action == "complete" {
				write(t, f.first.Root, "main.txt", "external resolution\n")
				command(t, f.first.Root, "add", "main.txt")
				command(t, f.first.Root, "commit", "--no-edit")
				resolution = "merge-completed-externally"
			} else {
				command(t, f.first.Root, "merge", "--abort")
			}
			saved, err := Save(f.first, Options{})
			requireOK(t, saved, err)
			receipt := assertReceipt(t, f.first, result.OperationID, "recovered")
			if receipt.Recovery == nil || receipt.Recovery.Resolution != resolution {
				t.Fatal("external resolution not recorded", receipt.Recovery)
			}
			assertParity(t, f)
			assertNoIncomplete(t, f.first)
		})
	}
}
