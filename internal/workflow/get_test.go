package workflow

import (
	"testing"

	"github.com/lewisl/wip-stream-go/internal/git"
)

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

// Scenarios from get-from-remote.test.js: all-branch synchronization and refusal.
func TestGetSynchronizesAllBranches(t *testing.T) {
	f := newFixture(t)
	start(t, f.second, "topic/slash")
	savedWork(t, f.second, "topic.txt", "topic\n")
	command(t, f.second.Root, "switch", "main")
	savedWork(t, f.second, "remote.txt", "remote\n")
	result := get(t, f.first)
	if result.Checkout != "main" || result.Published {
		t.Fatal(result)
	}
	assertParity(t, f)
	if command(t, f.first.Root, "config", "--get", "branch.topic/slash.remote") != "origin" || command(t, f.first.Root, "config", "--get", "branch.topic/slash.merge") != "refs/heads/topic/slash" {
		t.Fatal("missing imported tracking metadata")
	}
	if command(t, f.first.Root, "show", "topic/slash:topic.txt") != "topic" {
		t.Fatal("remote-only branch not imported")
	}
	assertReceipt(t, f.first, result.OperationID, "completed")
}

func TestGetUnsafeBranchPreventsOtherFastForwards(t *testing.T) {
	for _, condition := range []string{"local-only", "local-ahead", "diverged", "unsafe-remote-deletion"} {
		t.Run(condition, func(t *testing.T) {
			f := newFixture(t)
			start(t, f.first, "topic")
			savedWork(t, f.first, "topic.txt", "published\n")
			get(t, f.second)
			command(t, f.first.Root, "switch", "main")
			command(t, f.second.Root, "switch", "main")
			savedWork(t, f.second, "advance.txt", "remote main\n")
			switch condition {
			case "local-only":
				command(t, f.first.Root, "branch", "unpublished")
			case "local-ahead":
				command(t, f.first.Root, "switch", "topic")
				commitFile(t, f.first, "local.txt", "local\n")
				command(t, f.first.Root, "switch", "main")
			case "diverged":
				command(t, f.first.Root, "switch", "topic")
				commitFile(t, f.first, "local.txt", "local\n")
				command(t, f.first.Root, "switch", "main")
				command(t, f.second.Root, "switch", "topic")
				savedWork(t, f.second, "other.txt", "other\n")
			case "unsafe-remote-deletion":
				command(t, f.first.Root, "switch", "topic")
				commitFile(t, f.first, "local.txt", "local\n")
				command(t, f.first.Root, "switch", "main")
				command(t, f.second.Root, "push", "origin", ":topic")
			}
			unchangedRefusal(t, f, "UNSAFE_BRANCHES", func() (Result, error) { return Get(f.first) })
		})
	}
}

func TestGetDeletedCheckoutFallsBackToParentOrDefault(t *testing.T) {
	for _, deleteParent := range []bool{false, true} {
		name := "parent"
		if deleteParent {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			start(t, f.first, "parent")
			savedWork(t, f.first, "parent.txt", "parent\n")
			start(t, f.first, "child")
			savedWork(t, f.first, "child.txt", "child\n")
			get(t, f.second)
			deletions := []string{"push", "origin", ":child"}
			expected := "parent"
			if deleteParent {
				deletions = append(deletions, ":parent")
				expected = "main"
			}
			command(t, f.second.Root, deletions...)
			result := get(t, f.first)
			if result.Checkout != expected {
				t.Fatal(result)
			}
			assertAbsent(t, f.first, git.Local("child"))
			assertParity(t, f)
		})
	}
}
