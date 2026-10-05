package workflow

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
)

// Refusal assertions follow the extension's test/fixture-state.js. Remote
// tracking refs are excluded because fetching is allowed before refusing.
type observedState struct {
	Branch, Head, Refs, Remote, Config, Status, Staged, Unstaged string
	Files                                                        map[string]observedFile
	Receipts                                                     map[string]string
}
type observedFile struct {
	Contents string
	Mode     fs.FileMode
}

func assertFixtureTarget(t *testing.T, dir string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	for p := resolved; ; p = filepath.Dir(p) {
		if data, err := os.ReadFile(filepath.Join(p, "fixture-marker")); err == nil && string(data) == "disposable" {
			return
		}
		if filepath.Dir(p) == p {
			t.Fatalf("Git target is outside a disposable fixture: %s", dir)
		}
	}
}

func observe(t *testing.T, f fixture) observedState {
	t.Helper()
	r := f.first.Root
	state := observedState{
		Branch: command(t, r, "branch", "--show-current"), Head: command(t, r, "rev-parse", "HEAD"),
		Refs:   command(t, r, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/", "refs/wipstream/"),
		Remote: command(t, f.remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/"),
		Config: command(t, r, "config", "--local", "--list", "-z"), Status: command(t, r, "status", "--porcelain=v1", "-z"),
		Staged: command(t, r, "diff", "--cached", "--binary"), Unstaged: command(t, r, "diff", "--binary"),
		Files: map[string]observedFile{}, Receipts: map[string]string{},
	}
	err := filepath.WalkDir(r, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == r {
			return nil
		}
		if entry.Name() == ".git" && entry.IsDir() {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(r, p)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		content := ""
		if info.Mode()&os.ModeSymlink != 0 {
			content, err = os.Readlink(p)
		} else if info.Mode().IsRegular() {
			var b []byte
			b, err = os.ReadFile(p)
			content = string(b)
		}
		if err != nil {
			return err
		}
		state.Files[rel] = observedFile{content, info.Mode()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	common, err := f.first.CommonDir()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(common, "wipstream", "operations"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(common, "wipstream", "operations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		state.Receipts[entry.Name()] = string(b)
	}
	return state
}

func unchangedRefusal(t *testing.T, f fixture, code string, action func() (Result, error)) {
	t.Helper()
	before := observe(t, f)
	_, err := action()
	requireError(t, err, code)
	if after := observe(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s changed fixture state\nbefore: %#v\nafter: %#v", code, before, after)
	}
}

func commitFile(t *testing.T, r *git.Repository, name, content string) string {
	t.Helper()
	write(t, r.Root, name, content)
	command(t, r.Root, "add", "--all")
	command(t, r.Root, "commit", "-m", name)
	return command(t, r.Root, "rev-parse", "HEAD")
}

func savedWork(t *testing.T, r *git.Repository, name, content string) Result {
	t.Helper()
	write(t, r.Root, name, content)
	result, err := Save(r, options(name))
	return requireOK(t, result, err)
}

func start(t *testing.T, r *git.Repository, name string) Result {
	t.Helper()
	result, err := Start(r, name)
	return requireOK(t, result, err)
}

func get(t *testing.T, r *git.Repository) Result {
	t.Helper()
	result, err := Get(r)
	return requireOK(t, result, err)
}

func assertParity(t *testing.T, f fixture) {
	t.Helper()
	local := command(t, f.first.Root, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")
	remote := command(t, f.remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")
	if local != remote {
		t.Fatalf("branch parity differs\nlocal: %s\nremote: %s", local, remote)
	}
	if status := command(t, f.first.Root, "status", "--porcelain"); status != "" {
		t.Fatalf("expected clean worktree: %s", status)
	}
}

func assertAbsent(t *testing.T, r *git.Repository, ref string) {
	t.Helper()
	value, err := r.Object(ref)
	if err != nil {
		t.Fatal(err)
	}
	if value != nil {
		t.Fatalf("%s still exists: %s", ref, *value)
	}
}

func assertReceipt(t *testing.T, r *git.Repository, id, status string) *operations.Receipt {
	t.Helper()
	receipt, err := operations.Read(r, id)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != status {
		t.Fatalf("receipt %s status: got %s, want %s", id, receipt.Status, status)
	}
	return receipt
}

func assertNoIncomplete(t *testing.T, r *git.Repository) {
	t.Helper()
	receipts, err := operations.List(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range receipts {
		if receipt.Incomplete() {
			t.Fatalf("incomplete receipt %s (%s)", receipt.Plan.OperationID, receipt.Phase)
		}
	}
}

func requireMessage(t *testing.T, r Result, text string) {
	t.Helper()
	if !strings.Contains(r.Message, text) {
		t.Fatalf("message %q does not contain %q", r.Message, text)
	}
}
