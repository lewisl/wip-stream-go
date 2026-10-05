package workflow

import "testing"

// Scenarios from lifecycle-workflow.test.js and workflow-refusals.test.js.
func TestStartCarriesStagedUnstagedAndUntrackedWork(t *testing.T) {
	f := newFixture(t)
	root := f.first.Root
	write(t, root, "main.txt", "staged\n")
	command(t, root, "add", "main.txt")
	write(t, root, "main.txt", "unstaged\n")
	write(t, root, "draft.txt", "draft\n")
	before := observe(t, f)
	result := start(t, f.first, "feature/draft")
	after := observe(t, f)
	if after.Head != before.Head || after.Status != before.Status || after.Staged != before.Staged || after.Unstaged != before.Unstaged {
		t.Fatal("Start committed or changed carried work")
	}
	if result.Checkout != "feature/draft" || command(t, root, "config", "--get", "branch.feature/draft.wipstreamParent") != "main" {
		t.Fatal("parent or checkout not recorded")
	}
	if command(t, f.remote, "for-each-ref", "refs/heads/feature/draft") != "" {
		t.Fatal("Start unexpectedly published")
	}
	assertReceipt(t, f.first, result.OperationID, "completed")
}

func TestStartRefusals(t *testing.T) {
	for _, condition := range []string{"blank", "invalid", "option-like", "local-existing", "remote-existing", "detached"} {
		t.Run(condition, func(t *testing.T) {
			f := newFixture(t)
			name, code := "topic", "BRANCH_EXISTS"
			switch condition {
			case "blank":
				name = " "
				code = "INVALID_BRANCH"
			case "invalid":
				name = "invalid branch name"
				code = "INVALID_BRANCH"
			case "option-like":
				name = "--danger"
				code = "INVALID_BRANCH"
			case "local-existing":
				command(t, f.first.Root, "branch", name)
			case "remote-existing":
				start(t, f.second, name)
				savedWork(t, f.second, "topic.txt", "remote\n")
				command(t, f.first.Root, "fetch", "origin")
			case "detached":
				command(t, f.first.Root, "switch", "--detach")
				code = "DETACHED_HEAD"
			}
			unchangedRefusal(t, f, code, func() (Result, error) { return Start(f.first, name) })
		})
	}
}
