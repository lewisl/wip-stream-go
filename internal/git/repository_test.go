package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real-Git scenarios from the reference's git.test.js and atomic Save race.
func TestRawPreservesWhitespaceAndBinaryBytes(t *testing.T) {
	repo, _ := gitFixture(t)
	expected := []byte{' ', '\t', 0, 1, 255, '\n', '\n', ' '}
	hash, err := repo.Raw(expected, "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := repo.Raw(nil, "cat-file", "blob", strings.TrimSpace(string(hash)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatalf("Git output bytes changed: got %v, want %v", actual, expected)
	}
}

func TestLocalRefTransactionRejectsStaleObjectAtomically(t *testing.T) {
	repo, _ := gitFixture(t)
	old := gitCommand(t, repo.Root, "rev-parse", "HEAD")
	writeGitFile(t, repo.Root, "new.txt", "new\n")
	gitCommand(t, repo.Root, "add", ".")
	gitCommand(t, repo.Root, "commit", "-m", "advance")
	tip := gitCommand(t, repo.Root, "rev-parse", "HEAD")
	before := gitCommand(t, repo.Root, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")
	err := repo.UpdateRefs([]Update{{Ref: Local("main"), ExpectedOld: Ptr(old), Proposed: Ptr(tip)}, {Ref: Local("companion"), Proposed: Ptr(tip)}})
	if err == nil {
		t.Fatal("stale transaction accepted")
	}
	if after := gitCommand(t, repo.Root, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/"); after != before {
		t.Fatal("transaction partially changed refs", after)
	}
}

func TestRemotePushRejectsStaleLeasesAtomically(t *testing.T) {
	for _, condition := range []string{"changed-existing", "previously-absent"} {
		t.Run(condition, func(t *testing.T) {
			repo, remote := gitFixture(t)
			root := filepath.Dir(repo.Root)
			peer := filepath.Join(root, "peer")
			gitCommand(t, root, "clone", remote, peer)
			configureGit(t, peer)
			old := gitCommand(t, repo.Root, "rev-parse", "HEAD")
			writeGitFile(t, repo.Root, "local.txt", "local\n")
			gitCommand(t, repo.Root, "add", ".")
			gitCommand(t, repo.Root, "commit", "-m", "local")
			tip := gitCommand(t, repo.Root, "rev-parse", "HEAD")
			if condition == "previously-absent" {
				gitCommand(t, peer, "switch", "-c", "companion")
			}
			writeGitFile(t, peer, "winner.txt", "winner\n")
			gitCommand(t, peer, "add", ".")
			gitCommand(t, peer, "commit", "-m", "winner")
			branch := "main"
			if condition == "previously-absent" {
				branch = "companion"
			}
			gitCommand(t, peer, "push", "origin", branch)
			before := gitCommand(t, remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")
			err := repo.Push("origin", []RemoteUpdate{{Ref: Local("main"), Expected: Ptr(old), Proposed: Ptr(tip)}, {Ref: Local("companion"), Proposed: Ptr(tip)}}, false)
			if err == nil {
				t.Fatal("stale remote lease accepted")
			}
			if after := gitCommand(t, remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/"); after != before {
				t.Fatal("atomic rejection changed a companion branch", after)
			}
			if gitCommand(t, repo.Root, "rev-parse", "HEAD") != tip {
				t.Fatal("push failure lost local work")
			}
		})
	}
}

func TestLocalRefTransactionCreatesReplacesAndDeletes(t *testing.T) {
	repo, _ := gitFixture(t)
	old := gitCommand(t, repo.Root, "rev-parse", "HEAD")
	gitCommand(t, repo.Root, "branch", "replace")
	gitCommand(t, repo.Root, "branch", "delete")
	writeGitFile(t, repo.Root, "new.txt", "new\n")
	gitCommand(t, repo.Root, "add", ".")
	gitCommand(t, repo.Root, "commit", "-m", "advance")
	tip := gitCommand(t, repo.Root, "rev-parse", "HEAD")
	err := repo.UpdateRefs([]Update{{Ref: Local("create"), Proposed: Ptr(tip)}, {Ref: Local("replace"), ExpectedOld: Ptr(old), Proposed: Ptr(tip)}, {Ref: Local("delete"), ExpectedOld: Ptr(old)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"create", "replace"} {
		if gitCommand(t, repo.Root, "rev-parse", branch) != tip {
			t.Fatal("transaction did not update", branch)
		}
	}
	deleted, err := repo.Object(Local("delete"))
	if err != nil || deleted != nil {
		t.Fatal("transaction did not delete branch", deleted, err)
	}
}

func gitFixture(t *testing.T) (*Repository, string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	root := t.TempDir()
	writeGitFile(t, root, "fixture-marker", "disposable")
	remote, dir := filepath.Join(root, "remote.git"), filepath.Join(root, "worker")
	gitCommand(t, root, "init", "--bare", "--initial-branch=main", remote)
	gitCommand(t, root, "init", "--initial-branch=main", dir)
	configureGit(t, dir)
	writeGitFile(t, dir, "base.txt", "base\n")
	gitCommand(t, dir, "add", ".")
	gitCommand(t, dir, "commit", "-m", "base")
	gitCommand(t, dir, "remote", "add", "origin", remote)
	gitCommand(t, dir, "push", "-u", "origin", "main")
	repo, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, remote
}

func configureGit(t *testing.T, dir string) {
	t.Helper()
	gitCommand(t, dir, "config", "user.name", "Fixture")
	gitCommand(t, dir, "config", "user.email", "fixture@example.invalid")
}

func writeGitFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func gitCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for p := resolved; ; p = filepath.Dir(p) {
		b, err := os.ReadFile(filepath.Join(p, "fixture-marker"))
		if err == nil && string(b) == "disposable" {
			break
		}
		if filepath.Dir(p) == p {
			t.Fatalf("Git target outside fixture: %s", dir)
		}
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}
