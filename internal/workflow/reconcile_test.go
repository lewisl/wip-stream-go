package workflow

import "testing"

// Scenarios from conflict-workflow.test.js and workflow-refusals.test.js.
func TestReconcileCreatesAndPublishesMerge(t *testing.T) {
	f := newFixture(t)
	local := commitFile(t, f.first, "local.txt", "local\n")
	savedWork(t, f.second, "remote.txt", "remote\n")
	remote := command(t, f.remote, "rev-parse", "main")
	result, err := Reconcile(f.first, Options{})
	requireOK(t, result, err)
	if result.Pending || !result.Published {
		t.Fatal(result)
	}
	if parents := command(t, f.first.Root, "show", "-s", "--format=%P", "HEAD"); parents != local+" "+remote {
		t.Fatal("merge parents incorrect", parents)
	}
	if command(t, f.first.Root, "show", "HEAD:local.txt") != "local" || command(t, f.first.Root, "show", "HEAD:remote.txt") != "remote" {
		t.Fatal("merge content missing")
	}
	assertParity(t, f)
	assertReceipt(t, f.first, result.OperationID, "completed")
}

func TestReconcileRefusesNonDivergentBranches(t *testing.T) {
	for _, relation := range []string{"equal", "local-ahead", "remote-ahead"} {
		t.Run(relation, func(t *testing.T) {
			f := newFixture(t)
			if relation == "local-ahead" {
				commitFile(t, f.first, "local.txt", "local\n")
			}
			if relation == "remote-ahead" {
				savedWork(t, f.second, "remote.txt", "remote\n")
			}
			unchangedRefusal(t, f, "CURRENT_BRANCH_NOT_DIVERGED", func() (Result, error) { return Reconcile(f.first, Options{}) })
		})
	}
}

func TestReconcileRefusesAnotherDivergentBranch(t *testing.T) {
	f := newFixture(t)
	start(t, f.first, "topic")
	savedWork(t, f.first, "topic.txt", "topic\n")
	get(t, f.second)
	for _, branch := range []string{"main", "topic"} {
		command(t, f.first.Root, "switch", branch)
		command(t, f.second.Root, "switch", branch)
		commitFile(t, f.first, "local-"+branch+".txt", "local\n")
		commitFile(t, f.second, "remote-"+branch+".txt", "remote\n")
		command(t, f.second.Root, "push", "origin", branch)
	}
	command(t, f.first.Root, "switch", "main")
	unchangedRefusal(t, f, "OTHER_DIVERGENCE", func() (Result, error) { return Reconcile(f.first, Options{}) })
}
