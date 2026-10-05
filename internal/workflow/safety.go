package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
)

type LockRecord struct {
	SchemaVersion  int    `json:"schemaVersion"`
	OperationID    string `json:"operationId"`
	Command        string `json:"command"`
	PID            int    `json:"pid"`
	Hostname       string `json:"hostname"`
	StartedAt      string `json:"startedAt"`
	RepositoryRoot string `json:"repositoryRoot"`
}

func lockOwner(repo *git.Repository, command string) LockRecord {
	host, _ := os.Hostname()
	return LockRecord{1, operations.ID(), command, os.Getpid(), host, operations.Now(), repo.Root}
}
func readLock(p string) (*LockRecord, error) {
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	var l LockRecord
	if e = json.Unmarshal(b, &l); e != nil {
		return nil, e
	}
	if l.SchemaVersion != 1 || l.PID <= 0 || l.OperationID == "" || l.Command == "" || l.Hostname == "" || l.RepositoryRoot == "" || l.StartedAt == "" {
		return nil, fmt.Errorf("unverifiable command lock")
	}
	return &l, nil
}

const recoveryLease = "refs/wipstream/command-lock-recovery"

func acquire(repo *git.Repository, command string) (func() error, error) {
	if e := repo.SingleWorktree(); e != nil {
		return nil, e
	}
	common, e := repo.CommonDir()
	if e != nil {
		return nil, e
	}
	p := filepath.Join(common, "wipstream", "command.lock")
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return nil, e
	}
	lease, e := repo.Object(recoveryLease)
	if e != nil {
		return nil, e
	}
	if lease != nil {
		return nil, fmt.Errorf("COMMAND_IN_PROGRESS: command-lock recovery lease exists; run recover")
	}
	record := lockOwner(repo, command)
	b, e := json.MarshalIndent(record, "", "  ")
	if e != nil {
		return nil, e
	}
	f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(e) {
		l, readErr := readLock(p)
		if readErr != nil {
			return nil, fmt.Errorf("STALE_COMMAND_LOCK: unreadable lock preserved at %s", p)
		}
		host, _ := os.Hostname()
		if l.Hostname == host && !processAlive(l.PID) {
			return nil, fmt.Errorf("STALE_COMMAND_LOCK: %s; run recover", l.Command)
		}
		return nil, fmt.Errorf("COMMAND_IN_PROGRESS: %s (PID %d)", l.Command, l.PID)
	}
	if e != nil {
		return nil, e
	}
	_, e = f.Write(append(b, '\n'))
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return nil, e
	}
	if closeErr != nil {
		return nil, closeErr
	}
	release := func() error {
		l, e := readLock(p)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if *l != record {
			return fmt.Errorf("COMMAND_LOCK_CHANGED: lock preserved")
		}
		return os.Remove(p)
	}
	lease, e = repo.Object(recoveryLease)
	if e == nil && lease != nil {
		e = fmt.Errorf("COMMAND_IN_PROGRESS: lock recovery started")
	}
	if e == nil {
		e = repo.SingleWorktree()
	}
	if e != nil {
		_ = release()
		return nil, e
	}
	return release, nil
}
func RecoverLock(repo *git.Repository) (bool, error) {
	if e := repo.SingleWorktree(); e != nil {
		return false, e
	}
	common, e := repo.CommonDir()
	if e != nil {
		return false, e
	}
	p := filepath.Join(common, "wipstream", "command.lock")
	previous, e := repo.Object(recoveryLease)
	if e != nil {
		return false, e
	}
	host, _ := os.Hostname()
	if previous != nil {
		b, e := repo.Raw(nil, "cat-file", "blob", *previous)
		if e != nil {
			return false, e
		}
		var l LockRecord
		if e = json.Unmarshal(b, &l); e != nil || l.SchemaVersion != 1 || l.PID <= 0 || l.RepositoryRoot != repo.Root || l.Hostname != host || processAlive(l.PID) {
			return false, fmt.Errorf("COMMAND_IN_PROGRESS: recovery lease owner cannot be proved stale")
		}
	}
	owner := lockOwner(repo, "Recover command lock")
	b, e := json.Marshal(owner)
	if e != nil {
		return false, e
	}
	blob, e := repo.Mutate(b, "hash-object", "-w", "--stdin")
	if e != nil {
		return false, e
	}
	if e = repo.UpdateRefs([]git.Update{{Ref: recoveryLease, ExpectedOld: previous, Proposed: git.Ptr(blob)}}); e != nil {
		return false, e
	}
	defer repo.UpdateRefs([]git.Update{{Ref: recoveryLease, ExpectedOld: git.Ptr(blob)}})
	before, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return previous != nil, nil
	}
	if e != nil {
		return false, e
	}
	l, e := readLock(p)
	if e != nil {
		return false, e
	}
	if !before.Mode().IsRegular() || !ownedFile(before) || l.RepositoryRoot != repo.Root || l.Hostname != host || processAlive(l.PID) {
		return false, fmt.Errorf("COMMAND_IN_PROGRESS: lock owner cannot be proved stale")
	}
	if _, e = operations.List(repo); e != nil {
		return false, e
	}
	after, e := os.Lstat(p)
	if e != nil {
		return false, e
	}
	verified, e := readLock(p)
	if e != nil {
		return false, e
	}
	if !os.SameFile(before, after) || *l != *verified {
		return false, fmt.Errorf("COMMAND_LOCK_CHANGED: stale lock preserved")
	}
	if e = os.Rename(p, p+".recovered-"+owner.OperationID); e != nil {
		return false, e
	}
	return true, nil
}
func locked(repo *git.Repository, command string, recognizeExternal bool, fn func() error) (err error) {
	release, e := acquire(repo, command)
	if e != nil {
		return e
	}
	defer func() {
		if e := release(); err == nil {
			err = e
		}
	}()
	if recognizeExternal {
		receipts, e := operations.List(repo)
		if e != nil {
			return e
		}
		for _, r := range receipts {
			if r.Incomplete() && r.PendingMerge != nil {
				if _, e = externalMerge(repo, r); e != nil {
					return e
				}
			}
		}
	}
	return fn()
}
func preflight(repo *git.Repository, clean, submodules bool) error {
	if e := repo.SingleWorktree(); e != nil {
		return e
	}
	for _, check := range []struct{ arg, code string }{{"--is-bare-repository", "BARE_REPOSITORY"}, {"--is-shallow-repository", "SHALLOW_REPOSITORY"}} {
		s, e := repo.Run("rev-parse", check.arg)
		if e != nil {
			return e
		}
		if s == "true" {
			return fmt.Errorf("%s: normal clone with full history required", check.code)
		}
	}
	active, e := repo.Active()
	if e != nil {
		return e
	}
	if active {
		return fmt.Errorf("GIT_OPERATION_IN_PROGRESS: continue or abort the active operation")
	}
	conflicts, e := repo.Conflicts()
	if e != nil {
		return e
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("UNRESOLVED_CONFLICTS")
	}
	status, e := repo.Status()
	if e != nil {
		return e
	}
	if clean && status != "" {
		return fmt.Errorf("DIRTY_WORKTREE: clean working tree required")
	}
	if submodules {
		if _, e = repo.Run("submodule", "foreach", "--quiet", "--recursive", "test -z \"$(git status --porcelain=v1)\""); e != nil {
			return fmt.Errorf("DIRTY_SUBMODULES: %w", e)
		}
	}
	receipts, e := operations.List(repo)
	if e != nil {
		return e
	}
	for _, r := range receipts {
		if r.Incomplete() {
			return fmt.Errorf("INCOMPLETE_WIPSTREAM_OPERATION: %s (%s, %s); run recover", r.Plan.OperationID, r.Plan.Command, r.Phase)
		}
	}
	return nil
}
func selectedRemote(repo *git.Repository) (string, error) {
	remote, e := repo.ConfigOne("wipstream.remote")
	if e != nil {
		return "", e
	}
	if remote == "" {
		return "", fmt.Errorf("NOT_INITIALIZED: run wipstream init")
	}
	if _, e = repo.Run("remote", "get-url", remote); e != nil {
		return "", e
	}
	return remote, nil
}
func currentBranch(repo *git.Repository) (string, error) {
	b, e := repo.Branch()
	if e != nil {
		return "", e
	}
	if b == "" {
		return "", fmt.Errorf("DETACHED_HEAD: check out an ordinary branch")
	}
	if _, e = repo.Hash(git.Local(b)); e != nil {
		return "", e
	}
	return b, nil
}
func configChange(repo *git.Repository, key string, after []string) (operations.Configuration, error) {
	before, e := repo.Config(key)
	return operations.Configuration{Key: key, Before: before, After: after}, e
}
func addTracking(repo *git.Repository, p *operations.Plan, remote string, names []string) error {
	slices.Sort(names)
	for _, b := range names {
		for _, v := range []struct{ key, value string }{{"branch." + b + ".remote", remote}, {"branch." + b + ".merge", git.Local(b)}} {
			c, e := configChange(repo, v.key, []string{v.value})
			if e != nil {
				return e
			}
			if !slices.Equal(c.Before, c.After) {
				p.ConfigurationChanges = append(p.ConfigurationChanges, c)
			}
		}
	}
	return nil
}
