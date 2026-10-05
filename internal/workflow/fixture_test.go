package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lewisl/wip-stream-go/internal/git"
)

// Each test owns disposable ordinary clones and a local bare remote.
type fixture struct {
	root, remote  string
	first, second *git.Repository
}

func command(t *testing.T, dir string, args ...string) string {
	t.Helper()
	assertFixtureTarget(t, dir)
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "fixture-marker"), []byte("disposable"), 0600); e != nil {
		t.Fatal(e)
	}
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	command(t, root, "init", "--bare", "--initial-branch=main", remote)
	command(t, root, "init", "--initial-branch=main", seed)
	command(t, seed, "config", "user.name", "Fixture")
	command(t, seed, "config", "user.email", "fixture@example.invalid")
	write(t, seed, "main.txt", "base\n")
	command(t, seed, "add", ".")
	command(t, seed, "commit", "-m", "base")
	command(t, seed, "remote", "add", "origin", remote)
	command(t, seed, "push", "-u", "origin", "main")
	repos := []*git.Repository{}
	for _, name := range []string{"first", "second"} {
		dir := filepath.Join(root, name)
		command(t, root, "clone", remote, dir)
		command(t, dir, "config", "user.name", "Fixture")
		command(t, dir, "config", "user.email", "fixture@example.invalid")
		r, e := git.Open(context.Background(), dir)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = Init(r, Options{}); e != nil {
			t.Fatal(e)
		}
		repos = append(repos, r)
	}
	return fixture{root, remote, repos[0], repos[1]}
}

func write(t *testing.T, root, name, text string) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
}

func options(message string) Options {
	return Options{Message: func(string) (string, error) { return message, nil }, Confirm: func(string) (bool, error) { return true, nil }}
}

func requireOK(t *testing.T, r Result, e error) Result {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func requireError(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || !strings.Contains(e.Error(), code) {
		t.Fatalf("expected %s, got %v", code, e)
	}
}

func conflictingFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	r, e := Start(f.first, "feature")
	requireOK(t, r, e)
	write(t, f.first.Root, "main.txt", "feature\n")
	r, e = Save(f.first, options("feature"))
	requireOK(t, r, e)
	write(t, f.second.Root, "main.txt", "parent\n")
	r, e = Save(f.second, options("parent"))
	requireOK(t, r, e)
	return f
}
