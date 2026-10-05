package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteAdoptionPreservesIgnoredAndBacksUp(t *testing.T) {
	f := newFixture(t)
	write(t, f.first.Root, ".gitignore", "cache/\n")
	command(t, f.first.Root, "add", ".")
	command(t, f.first.Root, "commit", "-m", "ignore")
	r, e := Save(f.first, options("ignore"))
	requireOK(t, r, e)
	if e = os.Mkdir(filepath.Join(f.first.Root, "cache"), 0700); e != nil {
		t.Fatal(e)
	}
	write(t, f.first.Root, "cache/ignored.txt", "keep")
	write(t, f.first.Root, "main.txt", "discarded")
	write(t, f.first.Root, "untracked.txt", "remove")
	opts := Options{Authority: "remote", BackupParent: f.root}
	r, e = Init(f.first, opts)
	requireOK(t, r, e)
	b, e := os.ReadFile(filepath.Join(f.first.Root, "cache/ignored.txt"))
	if e != nil || string(b) != "keep" {
		t.Fatal("ignored work lost")
	}
	if _, e = os.Stat(filepath.Join(f.first.Root, "untracked.txt")); !os.IsNotExist(e) {
		t.Fatal("untracked entry not removed")
	}
	if !strings.Contains(r.Message, "verified backup") {
		t.Fatal(r)
	}
	_, e = Undo(f.first, options(""))
	requireError(t, e, "UNDO_NOT_ELIGIBLE")
}

func TestStaleApprovalRefusesChangedFiles(t *testing.T) {
	f := newFixture(t)
	write(t, f.first.Root, "main.txt", "before")
	opts := Options{InitChoice: func(string) (string, error) { write(t, f.first.Root, "main.txt", "after"); return "remote", nil }, Discard: true}
	_, e := Init(f.first, opts)
	requireError(t, e, "SETUP_STATE_CHANGED")
	b, e := os.ReadFile(filepath.Join(f.first.Root, "main.txt"))
	if e != nil || string(b) != "after" {
		t.Fatal("work discarded")
	}
}

func TestStaleApprovalRefusesChangedIntentToAdd(t *testing.T) {
	f := newFixture(t)
	write(t, f.first.Root, "empty.txt", "")
	command(t, f.first.Root, "add", "--intent-to-add", "empty.txt")
	opts := Options{InitChoice: func(string) (string, error) {
		command(t, f.first.Root, "add", "empty.txt")
		return "remote", nil
	}, Discard: true}
	_, err := Init(f.first, opts)
	requireError(t, err, "SETUP_STATE_CHANGED")
}

// Scenarios from public-command-sequences.test.js and initialize-repository.test.js.
func TestInitWithoutRemoteHeadCache(t *testing.T) {
	for _, authority := range []string{"local-work", "remote"} {
		t.Run(authority, func(t *testing.T) {
			f := newFixture(t)
			root := f.first.Root
			command(t, root, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
			command(t, root, "config", "--unset", "wipstream.remote")
			write(t, root, "draft.txt", "local draft\n")
			opts := options("setup checkpoint")
			opts.Authority = authority
			opts.Discard = true
			result, err := Init(f.first, opts)
			requireOK(t, result, err)
			assertParity(t, f)
			if command(t, root, "symbolic-ref", "refs/remotes/origin/HEAD") != "refs/remotes/origin/main" || command(t, root, "config", "--get", "wipstream.remote") != "origin" {
				t.Fatal("initialization did not restore selection and default cache")
			}
			if authority == "local-work" {
				if !result.CheckpointCreated || command(t, f.remote, "show", "main:draft.txt") != "local draft" {
					t.Fatal("local setup work not published")
				}
			} else if _, err := os.Stat(filepath.Join(root, "draft.txt")); !os.IsNotExist(err) {
				t.Fatal("remote setup did not remove approved untracked file")
			}
			assertReceipt(t, f.first, result.OperationID, "completed")
		})
	}
}

func TestInitAuthorityRefusalsPreserveWork(t *testing.T) {
	for _, choice := range []string{"missing", "cancel", "reconcile", "invalid"} {
		t.Run(choice, func(t *testing.T) {
			f := newFixture(t)
			write(t, f.first.Root, "draft.txt", "draft\n")
			opts := Options{Authority: choice}
			code := "INVALID_AUTHORITY"
			switch choice {
			case "missing":
				opts.Authority = ""
				code = "AUTHORITY_CHOICE_REQUIRED"
			case "cancel":
				code = "CANCELLED"
			case "reconcile":
				code = "RECONCILIATION_REQUIRED"
			}
			unchangedRefusal(t, f, code, func() (Result, error) { return Init(f.first, opts) })
		})
	}
}

func TestInitRemoteReplacementRequiresBackupOrDiscardApproval(t *testing.T) {
	f := newFixture(t)
	write(t, f.first.Root, "main.txt", "must survive\n")
	unchangedRefusal(t, f, "REMOTE_DISCARD_NOT_CONFIRMED", func() (Result, error) { return Init(f.first, Options{Authority: "remote"}) })
}
