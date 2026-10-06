package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
)

func TestHumanInitReportsChosenHandoff(t *testing.T) {
	for _, condition := range []string{"checkpoint", "existing-commit", "already-matching", "remote", "remote-backup"} {
		t.Run(condition, func(t *testing.T) {
			f := cliFixture(t)
			before := cliGit(t, f.remote, "rev-parse", "main")
			args := []string{"init"}
			expected := []string{"Initialized repository", `"origin"`, `checked out "main"`}
			switch condition {
			case "checkpoint":
				cliWrite(t, f.first, "draft.txt", "publish me\n")
				args = append(args, "--authority", "local-work", "-m", "setup checkpoint")
				expected = append(expected, "Created a checkpoint", `Pushed "main" to "origin"`, "Safe to resume")
			case "existing-commit":
				cliWrite(t, f.first, "draft.txt", "publish me\n")
				cliGit(t, f.first, "add", ".")
				cliGit(t, f.first, "commit", "-m", "already committed")
				args = append(args, "--authority", "local-work")
				expected = append(expected, "No new checkpoint was needed", `Pushed "main" to "origin"`, "Safe to resume")
			case "already-matching":
				expected = append(expected, "No new checkpoint was needed", "No push was needed", "All branches are synchronized")
			case "remote", "remote-backup":
				cliWrite(t, f.first, "main.txt", "discard me\n")
				args = append(args, "--authority", "remote")
				expected = []string{"Initialized repository using remote", `"origin"`, "Replaced local branches and working files", `Checked out "main"`, "No commit or push was made"}
				if condition == "remote-backup" {
					args = append(args, "--backup-parent", f.root)
					expected = append(expected, "verified backup at")
				} else {
					args = append(args, "--discard-local-work")
				}
			}
			humanOK(t, f.first, "SUCCESS:", expected, args...)
			if condition == "checkpoint" || condition == "existing-commit" {
				if cliGit(t, f.remote, "show", "main:draft.txt") != "publish me" || cliGit(t, f.remote, "rev-parse", "main") != cliGit(t, f.first, "rev-parse", "HEAD") {
					t.Fatal("message claimed publication without remote contents")
				}
			} else if cliGit(t, f.remote, "rev-parse", "main") != before {
				t.Fatal("message claimed no push but remote changed")
			}
			if cliGit(t, f.first, "status", "--porcelain") != "" {
				t.Fatal("successful initialization left dirty files")
			}
		})
	}
}

func TestHumanLifecycleMessages(t *testing.T) {
	for _, disposition := range []string{"retain", "delete"} {
		t.Run(disposition, func(t *testing.T) {
			f := cliFixture(t)
			execute(t, f.first, "init")
			humanOK(t, f.first, "SUCCESS:", []string{"Already up to date", `checked out "main"`}, "get")
			humanOK(t, f.first, "SUCCESS:", []string{`Started "feature" from parent "main"`, "No commit or push was made"}, "start", "feature")
			humanOK(t, f.first, "SUCCESS:", []string{"No new checkpoint", `Pushed "feature" to "origin"`}, "save")
			humanOK(t, f.first, "SUCCESS:", []string{`Branch "feature" already contains parent "main"`, "no merge was needed"}, "update")
			for _, value := range []string{"one", "two"} {
				cliWrite(t, f.first, "work.txt", value+"\n")
				humanOK(t, f.first, "SUCCESS:", []string{`Created a checkpoint on "feature"`, `Pushed "feature" to "origin"`, "Safe to resume"}, "save", "-m", value)
			}
			before := cliGit(t, f.first, "rev-parse", "HEAD")
			humanOK(t, f.first, "SUCCESS:", []string{`Condensed 2 commits on "feature" into one commit`, "same final files", `Pushed the rewritten branch to "origin"`}, "condense", "-m", "final", "--yes")
			humanOK(t, f.first, "SUCCESS:", []string{`Undid "Condense Branch"`, `Checked out "feature"`}, "undo", "--yes")
			if cliGit(t, f.first, "rev-parse", "HEAD") != before {
				t.Fatal("Undo output did not match branch state")
			}
			verb := "Retained"
			if disposition == "delete" {
				verb = "Deleted"
			}
			cliWrite(t, f.first, "last.txt", "last checkpoint\n")
			humanOK(t, f.first, "SUCCESS:", []string{`Finished "feature" into "main" locally and on remote "origin"`, "Created and published a checkpoint", verb + ` "feature" locally and remotely`, `checked out "main"`}, "finish", "--disposition", disposition, "-m", "last", "--yes")
			if cliGit(t, f.remote, "show", "main:last.txt") != "last checkpoint" {
				t.Fatal("Finish message claimed an unpublished checkpoint")
			}
		})
	}
}

func TestHumanGetListsActualBranchChanges(t *testing.T) {
	f := cliFixture(t)
	execute(t, f.first, "init")
	execute(t, f.second, "init")
	execute(t, f.first, "start", "deleted")
	execute(t, f.first, "save")
	cliGit(t, f.first, "switch", "main")
	execute(t, f.second, "get")
	execute(t, f.second, "start", "new-topic")
	cliWrite(t, f.second, "topic.txt", "topic\n")
	execute(t, f.second, "save", "-m", "topic")
	cliGit(t, f.second, "switch", "main")
	cliWrite(t, f.second, "remote.txt", "remote\n")
	execute(t, f.second, "save", "-m", "remote")
	cliGit(t, f.second, "push", "origin", ":deleted")
	humanOK(t, f.first, "SUCCESS:", []string{`Retrieved from "origin"`, `created "new-topic"`, `updated "main"`, `deleted "deleted"`, "All branches are synchronized"}, "get")
	if cliGit(t, f.first, "show", "new-topic:topic.txt") != "topic" || cliGit(t, f.first, "show", "main:remote.txt") != "remote" || cliGit(t, f.first, "for-each-ref", "refs/heads/deleted") != "" {
		t.Fatal("Get message did not match actual refs and files")
	}
}

func TestHumanUpdateReportsLocalMerge(t *testing.T) {
	f := cliFixture(t)
	execute(t, f.first, "init")
	execute(t, f.second, "init")
	execute(t, f.first, "start", "feature")
	cliWrite(t, f.first, "feature.txt", "feature\n")
	execute(t, f.first, "save", "-m", "feature")
	remoteBefore := cliGit(t, f.remote, "rev-parse", "feature")
	cliWrite(t, f.second, "parent.txt", "parent\n")
	execute(t, f.second, "save", "-m", "parent")
	humanOK(t, f.first, "SUCCESS:", []string{`Merged parent "main" into "feature"`, "The merge is local", "wipstream save"}, "update")
	if cliGit(t, f.remote, "rev-parse", "feature") != remoteBefore || cliGit(t, f.first, "rev-parse", "HEAD") == remoteBefore {
		t.Fatal("Update message did not match local-only merge")
	}
}

func TestHumanReconcileReportsMergeAndPush(t *testing.T) {
	f := divergedOutputFixture(t)
	humanOK(t, f.first, "SUCCESS:", []string{`Merged remote history into "main"`, `Pushed "main" to "origin"`, "Safe to resume"}, "reconcile")
	if cliGit(t, f.remote, "show", "main:local.txt") != "local" || cliGit(t, f.remote, "show", "main:remote.txt") != "remote" {
		t.Fatal("Reconcile message did not match published merge")
	}
}

func TestHumanPendingContinueAndAbortMessages(t *testing.T) {
	for _, action := range []string{"continue", "abort", "external-complete", "external-abort"} {
		t.Run(action, func(t *testing.T) {
			f := cliFixture(t)
			execute(t, f.first, "init")
			execute(t, f.second, "init")
			execute(t, f.first, "start", "feature")
			cliWrite(t, f.first, "main.txt", "feature\n")
			execute(t, f.first, "save", "-m", "feature")
			cliWrite(t, f.second, "main.txt", "parent\n")
			execute(t, f.second, "save", "-m", "parent")
			before := cliGit(t, f.first, "rev-parse", "HEAD")
			humanOK(t, f.first, "PENDING:", []string{`Merge on "feature" needs attention`, "Conflicts: main.txt", "Resolve and stage", "wipstream continue", "wipstream abort"}, "update")
			switch action {
			case "continue":
				cliWrite(t, f.first, "main.txt", "resolved\n")
				cliGit(t, f.first, "add", "main.txt")
				humanOK(t, f.first, "SUCCESS:", []string{`Committed the resolved merge on "feature"`, `Pushed "feature" to "origin"`, "Safe to resume"}, "continue")
				if cliGit(t, f.remote, "show", "feature:main.txt") != "resolved" {
					t.Fatal("Continue did not publish the resolution")
				}
			case "abort":
				humanOK(t, f.first, "SUCCESS:", []string{`Aborted the merge on "feature"`, "restored the verified pre-merge state"}, "abort")
				if cliGit(t, f.first, "rev-parse", "HEAD") != before {
					t.Fatal("Abort did not restore the recorded head")
				}
			case "external-complete":
				cliWrite(t, f.first, "main.txt", "resolved\n")
				cliGit(t, f.first, "add", "main.txt")
				cliGit(t, f.first, "commit", "--no-edit")
				completed := cliGit(t, f.first, "rev-parse", "HEAD")
				humanOK(t, f.first, "SUCCESS:", []string{"Git already completed the merge", "Kept current work", "wipstream save"}, "abort")
				if cliGit(t, f.first, "rev-parse", "HEAD") != completed {
					t.Fatal("recognizing completion reset a finished merge")
				}
			case "external-abort":
				cliGit(t, f.first, "merge", "--abort")
				humanOK(t, f.first, "SUCCESS:", []string{"Git already aborted the merge", "verified the pre-merge state"}, "abort")
			}
		})
	}
}

func TestHumanRecoverAndJSONReceiptIdentity(t *testing.T) {
	f := cliFixture(t)
	initialized := execute(t, f.first, "init")
	if initialized.OperationID == "" || strings.Contains(initialized.Message, initialized.OperationID) {
		t.Fatal("JSON must keep receipt identity separate from its human message")
	}
	repo, err := git.Open(context.Background(), f.first)
	if err != nil {
		t.Fatal(err)
	}
	plan := operations.NewPlan("Get from Remote")
	plan.Checkout = operations.Checkout{Before: "main", After: "main"}
	receipt, err := operations.Begin(repo, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = operations.Phase(repo, receipt, "before-remote-fetch"); err != nil {
		t.Fatal(err)
	}
	cliWrite(t, f.first, "draft.txt", "keep me\n")
	before := cliGit(t, f.first, "status", "--porcelain")
	humanOK(t, f.first, "SUCCESS:", []string{`Closed the interrupted "Get from Remote" attempt`, "kept current files", "Remote synchronization remains unverified", "retry 'wipstream save'"}, "recover", "--operation", plan.OperationID, "--yes")
	if cliGit(t, f.first, "status", "--porcelain") != before {
		t.Fatal("Recover message did not preserve current work")
	}
}

func TestHumanMergeDoesNotClaimSuccessWhenHandoffFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX executable Git hook")
	}
	for _, command := range []string{"reconcile", "continue"} {
		t.Run(command, func(t *testing.T) {
			f := divergedOutputFixture(t)
			fragment := "Merged remote history"
			if command == "continue" {
				cliWrite(t, f.first, "remote.txt", "conflict\n")
				cliGit(t, f.first, "add", "remote.txt")
				cliGit(t, f.first, "commit", "-m", "conflicting work")
				pending := execute(t, f.first, "reconcile")
				if !pending.Pending {
					t.Fatal("fixture needs a pending merge")
				}
				cliWrite(t, f.first, "remote.txt", "resolved\n")
				cliGit(t, f.first, "add", "remote.txt")
				fragment = "Committed the resolved merge"
			}
			remoteBefore := cliGit(t, f.remote, "rev-parse", "main")
			hook := filepath.Join(f.first, ".git", "hooks", "pre-push")
			if err := os.WriteFile(hook, []byte("#!/bin/sh\necho publication-blocked >&2\nexit 1\n"), 0755); err != nil {
				t.Fatal(err)
			}
			out, diagnostic, err := runHuman(t, f.first, command)
			if err == nil || out != "" || strings.Contains(diagnostic, "SUCCESS") || !strings.Contains(diagnostic, fragment) || !strings.Contains(diagnostic, "did not complete") || !strings.Contains(diagnostic, "Do not resume") {
				t.Fatal("failed handoff was reported as success", out, diagnostic, err)
			}
			if cliGit(t, f.remote, "rev-parse", "main") != remoteBefore || cliGit(t, f.first, "show", "HEAD:local.txt") != "local" || cliGit(t, f.first, "status", "--porcelain") != "" {
				t.Fatal("failed handoff must retain the completed local merge without publishing it")
			}
			repo, openErr := git.Open(context.Background(), f.first)
			if openErr != nil {
				t.Fatal(openErr)
			}
			receipts, readErr := operations.List(repo)
			if readErr != nil {
				t.Fatal(readErr)
			}
			incompleteID, completedMergeID := "", ""
			for _, receipt := range receipts {
				if receipt.Incomplete() {
					incompleteID = receipt.Plan.OperationID
				}
				if receipt.Plan.Command == "Reconcile with Remote" && receipt.Status == "completed" {
					completedMergeID = receipt.Plan.OperationID
				}
			}
			if incompleteID == "" || completedMergeID == "" || !strings.Contains(err.Error(), "wipstream recover --operation "+incompleteID) || strings.Contains(err.Error(), completedMergeID) {
				t.Fatal("recovery hint must select the failed handoff, not the completed merge", err)
			}
		})
	}
}

func TestHumanFinishFailureReportsCheckpointBeforeParentAdvance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX executable Git hook")
	}
	f := cliFixture(t)
	execute(t, f.first, "init")
	execute(t, f.first, "start", "feature")
	execute(t, f.first, "save")
	parent := cliGit(t, f.first, "rev-parse", "main")
	remoteFeature := cliGit(t, f.remote, "rev-parse", "feature")
	cliWrite(t, f.first, "draft.txt", "keep locally\n")
	hook := filepath.Join(f.first, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	out, diagnostic, err := runHuman(t, f.first, "finish", "--disposition", "retain", "-m", "last checkpoint", "--yes")
	if err == nil || out != "" || strings.Contains(diagnostic, "SUCCESS") || !strings.Contains(diagnostic, `Finish stopped before advancing parent "main"`) || !strings.Contains(diagnostic, "Checkpoint committed locally") {
		t.Fatal("failed Finish must describe its retained checkpoint", out, diagnostic, err)
	}
	if cliGit(t, f.first, "show", "HEAD:draft.txt") != "keep locally" || cliGit(t, f.first, "rev-parse", "main") != parent || cliGit(t, f.remote, "rev-parse", "main") != parent || cliGit(t, f.remote, "rev-parse", "feature") != remoteFeature {
		t.Fatal("Finish message did not match local checkpoint and unchanged parent/remote")
	}
}

func TestHumanInitFailureKeepsCheckpointAndOmitsSuccess(t *testing.T) {
	f := cliFixture(t)
	execute(t, f.second, "init")
	cliWrite(t, f.second, "remote.txt", "remote\n")
	execute(t, f.second, "save", "-m", "remote")
	remote := cliGit(t, f.remote, "rev-parse", "main")
	cliWrite(t, f.first, "local.txt", "local\n")
	out, diagnostic, err := runHuman(t, f.first, "init", "--authority", "local-work", "-m", "setup checkpoint")
	if err == nil || out != "" || strings.Contains(diagnostic, "SUCCESS") || !strings.Contains(diagnostic, "Initialization did not complete") || !strings.Contains(diagnostic, "Checkpoint committed locally") {
		t.Fatal("failed initialization message", out, diagnostic, err)
	}
	if cliGit(t, f.first, "show", "HEAD:local.txt") != "local" || cliGit(t, f.remote, "rev-parse", "main") != remote {
		t.Fatal("failed Init message did not match retained checkpoint and unchanged remote")
	}
}

func divergedOutputFixture(t *testing.T) fixture {
	t.Helper()
	f := cliFixture(t)
	execute(t, f.first, "init")
	execute(t, f.second, "init")
	cliWrite(t, f.first, "local.txt", "local\n")
	cliGit(t, f.first, "add", ".")
	cliGit(t, f.first, "commit", "-m", "local")
	cliWrite(t, f.second, "remote.txt", "remote\n")
	execute(t, f.second, "save", "-m", "remote")
	return f
}

func runHuman(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	root := New()
	var out, diagnostic bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&diagnostic)
	root.SetArgs(append([]string{"-C", dir}, args...))
	err := root.Execute()
	return out.String(), diagnostic.String(), err
}

func humanOK(t *testing.T, dir, status string, fragments []string, args ...string) string {
	t.Helper()
	out, diagnostic, err := runHuman(t, dir, args...)
	if err != nil || diagnostic != "" {
		t.Fatalf("wipstream %v: %v\n%s", args, err, diagnostic)
	}
	if !strings.HasPrefix(out, status+" ") || strings.Contains(out, "Operation:") {
		t.Fatalf("unexpected status or routine receipt identity: %q", out)
	}
	repo, openErr := git.Open(context.Background(), dir)
	if openErr != nil {
		t.Fatal(openErr)
	}
	receipts, readErr := operations.List(repo)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, receipt := range receipts {
		if strings.Contains(out, receipt.Plan.OperationID) {
			t.Fatalf("routine output contains receipt identity: %q", out)
		}
	}
	if status == "PENDING:" && strings.Contains(out, "SUCCESS") {
		t.Fatal("pending merge reported as completed", out)
	}
	for _, fragment := range fragments {
		if !strings.Contains(out, fragment) {
			t.Fatalf("wipstream %v: missing %q in %q", args, fragment, out)
		}
	}
	return out
}
