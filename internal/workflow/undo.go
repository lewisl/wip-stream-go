package workflow

import (
	"fmt"
	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var undoable = []string{"Get from Remote", "Initialize Repository", "Commit and Save", "Finish Branch", "Condense Branch", "Update from Parent"}

func afterUpdates(r *operations.Receipt) ([]git.Update, error) {
	updates := append([]git.Update{}, r.Plan.LocalRefUpdates...)
	if r.Outcome != nil {
		updates = append(updates, r.Outcome.AdditionalLocalRefUpdates...)
	}
	if c := r.Plan.Checkpoint; c != nil {
		updates = append(updates, git.Update{Ref: git.Local(c.Branch), ExpectedOld: git.Ptr(c.Before), Proposed: git.Ptr(c.After)})
	}
	byRef := map[string]git.Update{}
	result := []git.Update{}
	for _, u := range updates {
		old, exists := byRef[u.Ref]
		if exists {
			if !git.Same(old.ExpectedOld, u.ExpectedOld) || !git.Same(old.Proposed, u.Proposed) {
				return nil, fmt.Errorf("AMBIGUOUS_UNDO_PLAN: %s", u.Ref)
			}
			continue
		}
		byRef[u.Ref] = u
		result = append(result, u)
	}
	return result, nil
}
func undoCandidate(repo *git.Repository) (*operations.Receipt, error) {
	receipts, e := operations.List(repo)
	if e != nil {
		return nil, e
	}
	terminal := []*operations.Receipt{}
	for _, r := range receipts {
		if r.Incomplete() {
			return nil, fmt.Errorf("UNDO_NOT_ELIGIBLE: operation incomplete")
		}
		if slices.Contains([]string{"completed", "undone", "recovered"}, r.Status) {
			terminal = append(terminal, r)
		}
	}
	slices.SortFunc(terminal, func(a, b *operations.Receipt) int { return strings.Compare(b.CompletedAt, a.CompletedAt) })
	if len(terminal) == 0 {
		return nil, fmt.Errorf("UNDO_NOT_ELIGIBLE: no completed operation")
	}
	r := terminal[0]
	if r.Status != "completed" || !slices.Contains(undoable, r.Plan.Command) || len(r.Plan.RemoteAdoption) > 0 {
		return nil, fmt.Errorf("UNDO_NOT_ELIGIBLE: latest operation cannot be undone")
	}
	status, e := repo.Status()
	if e != nil {
		return nil, e
	}
	if status != "" {
		return nil, fmt.Errorf("UNDO_NOT_ELIGIBLE: working tree changed")
	}
	branch, e := repo.Branch()
	if e != nil {
		return nil, e
	}
	if branch != r.Plan.Checkout.After {
		return nil, fmt.Errorf("UNDO_NOT_ELIGIBLE: checkout changed")
	}
	refs, e := repo.Refs("refs/heads/")
	if e != nil {
		return nil, e
	}
	if r.Outcome != nil && r.Outcome.CompletedLocalRefs != nil && !slices.Equal(refs, r.Outcome.CompletedLocalRefs) {
		return nil, fmt.Errorf("UNDO_NOT_ELIGIBLE: ordinary local refs changed")
	}
	updates, e := afterUpdates(r)
	if e != nil {
		return nil, e
	}
	for _, u := range updates {
		actual, e := repo.Object(u.Ref)
		if e != nil {
			return nil, e
		}
		if !git.Same(actual, u.Proposed) {
			return nil, fmt.Errorf("UNDO_NOT_ELIGIBLE: ref %s changed", u.Ref)
		}
	}
	for _, c := range r.Plan.ConfigurationChanges {
		v, e := repo.Config(c.Key)
		if e != nil {
			return nil, e
		}
		if !slices.Equal(v, c.After) {
			return nil, fmt.Errorf("UNDO_NOT_ELIGIBLE: configuration %s changed", c.Key)
		}
	}
	if h := r.Plan.RemoteHead; h != nil {
		v, e := repo.Symbolic(git.Tracking(h.Remote, "HEAD"))
		if e != nil {
			return nil, e
		}
		if !git.Same(v, h.After) {
			return nil, fmt.Errorf("UNDO_NOT_ELIGIBLE: remote default cache changed")
		}
	}
	return r, nil
}
func Undo(repo *git.Repository, opts Options) (result Result, err error) {
	err = locked(repo, "Undo", true, func() error {
		if e := repo.SingleWorktree(); e != nil {
			return e
		}
		active, e := repo.Active()
		if e != nil {
			return e
		}
		conflicts, e := repo.Conflicts()
		if e != nil {
			return e
		}
		if active || len(conflicts) > 0 {
			return fmt.Errorf("GIT_OPERATION_IN_PROGRESS")
		}
		original, e := undoCandidate(repo)
		if e != nil {
			return e
		}
		if e = confirm(opts, "Undo "+original.Plan.Command+" ("+original.Plan.OperationID+")?"); e != nil {
			return e
		}
		remote, e := repo.ConfigOne("wipstream.remote")
		if e != nil {
			return e
		}
		if remote == "" {
			remote = "origin"
		}
		if e = repo.Fetch(remote); e != nil {
			return e
		}
		if original.Outcome != nil && original.Outcome.CompletedRemoteRefs != nil {
			recorded := []git.Ref{}
			for _, r := range original.Outcome.CompletedRemoteRefs {
				if strings.HasPrefix(r.Ref, git.Tracking(remote, "")) {
					recorded = append(recorded, r)
				}
			}
			current, e := repo.Refs(git.Tracking(remote, ""))
			if e != nil {
				return e
			}
			if !slices.Equal(recorded, current) {
				return fmt.Errorf("REMOTE_CHANGED_AFTER_OPERATION")
			}
		}
		// Recheck approval after the fetch and before entering any recorded boundary.
		latest, e := undoCandidate(repo)
		if e != nil {
			return e
		}
		if latest.Plan.OperationID != original.Plan.OperationID {
			return fmt.Errorf("UNDO_NOT_ELIGIBLE: latest action changed")
		}
		p := operations.NewPlan("Undo " + original.Plan.Command)
		p.Checkout = operations.Checkout{Before: original.Plan.Checkout.After, After: original.Plan.Checkout.Before}
		for _, u := range original.Plan.RemoteRefUpdates {
			actual, e := repo.Object(git.Tracking(remote, strings.TrimPrefix(u.Ref, "refs/heads/")))
			if e != nil {
				return e
			}
			if !git.Same(actual, u.Proposed) {
				return fmt.Errorf("REMOTE_CHANGED_AFTER_OPERATION: %s", u.Ref)
			}
			p.RemoteRefUpdates = append(p.RemoteRefUpdates, git.RemoteUpdate{Ref: u.Ref, Expected: u.Proposed, Proposed: u.Expected})
		}
		updates, e := afterUpdates(original)
		if e != nil {
			return e
		}
		for _, u := range updates {
			p.LocalRefUpdates = append(p.LocalRefUpdates, git.Update{Ref: u.Ref, ExpectedOld: u.Proposed, Proposed: u.ExpectedOld})
		}
		for i := len(original.Plan.ConfigurationChanges) - 1; i >= 0; i-- {
			c := original.Plan.ConfigurationChanges[i]
			p.ConfigurationChanges = append(p.ConfigurationChanges, operations.Configuration{Key: c.Key, Before: c.After, After: c.Before})
		}
		if c := original.Plan.Checkpoint; c != nil {
			p.CheckpointRestoration = &operations.Restoration{Before: c.Before, After: c.After}
		}
		if h := original.Plan.RemoteHead; h != nil {
			p.RemoteHead = &operations.RemoteHead{Remote: h.Remote, Before: h.After, After: h.Before}
		}
		result = Result{OperationID: p.OperationID, Checkout: p.Checkout.After, Message: "Undid " + original.Plan.Command}
		return operations.Recorded(repo, p, func(r *operations.Receipt) error {
			if len(p.RemoteRefUpdates) > 0 {
				if e := operations.Boundary(repo, r, "remote-push", func() error { return repo.Push(remote, p.RemoteRefUpdates, false) }); e != nil {
					return e
				}
				if e := operations.Boundary(repo, r, "remote-fetch", func() error { return repo.Fetch(remote) }); e != nil {
					return e
				}
			}
			branch, e := repo.Branch()
			if e != nil {
				return e
			}
			changing := false
			for _, u := range p.LocalRefUpdates {
				if u.Ref == git.Local(branch) {
					changing = true
				}
			}
			if changing {
				if e = mutation(repo, r, "checkout", "switch", "--detach"); e != nil {
					return e
				}
			}
			if len(p.LocalRefUpdates) > 0 {
				if e = operations.ApplyLocal(repo, r); e != nil {
					return e
				}
			}
			if p.Checkout.After != "" {
				if e = mutation(repo, r, "checkout", "switch", p.Checkout.After); e != nil {
					return e
				}
			}
			if e = operations.ApplyConfig(repo, r); e != nil {
				return e
			}
			if c := p.CheckpointRestoration; c != nil {
				if e = operations.Boundary(repo, r, "checkpoint-restoration", func() error { return restoreChanges(repo, c.Before, c.After) }); e != nil {
					return e
				}
			}
			if e = operations.ApplyHead(repo, r); e != nil {
				return e
			}
			if e = operations.Complete(repo, r); e != nil {
				return e
			}
			return operations.Close(repo, original, "undone", nil)
		})
	})
	return
}
func restoreChanges(repo *git.Repository, before, after string) error {
	patch, e := repo.Raw(nil, "diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", before, after, "--")
	if e != nil {
		return e
	}
	if len(patch) > 0 {
		if _, e = repo.Mutate(patch, "apply", "--whitespace=nowarn"); e != nil {
			return e
		}
	}
	changed, e := repo.Raw(nil, "diff", "--name-only", "--no-renames", "-z", before, after, "--")
	if e != nil {
		return e
	}
	tree, e := repo.Raw(nil, "ls-tree", "-r", "-z", "--full-tree", after)
	if e != nil {
		return e
	}
	entries := map[string][]string{}
	for _, line := range git.NulList(tree) {
		f := strings.SplitN(line, "\t", 2)
		if len(f) != 2 {
			return fmt.Errorf("invalid tree output")
		}
		entries[f[1]] = strings.Fields(f[0])
	}
	filemode, e := repo.ConfigOne("core.filemode")
	if e != nil {
		return e
	}
	for _, name := range git.NulList(changed) {
		expected, exists := entries[name]
		p := filepath.Join(repo.Root, filepath.FromSlash(name))
		info, e := os.Lstat(p)
		if !exists && os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if !exists {
			return fmt.Errorf("CHECKPOINT_RESTORATION_FAILED: path %s should be absent", name)
		}
		var actual string
		if info.Mode()&os.ModeSymlink != 0 {
			target, e := os.Readlink(p)
			if e != nil {
				return e
			}
			b, e := repo.Raw([]byte(target), "hash-object", "--stdin")
			if e != nil {
				return e
			}
			actual = strings.TrimSpace(string(b))
		} else if info.Mode().IsRegular() {
			actual, e = repo.Run("hash-object", "--path="+name, "--", name)
			if e != nil {
				return e
			}
		}
		modeMatches := expected[0] == "120000" && info.Mode()&os.ModeSymlink != 0 || expected[0] != "120000" && info.Mode().IsRegular() && (filemode == "false" || ((info.Mode().Perm()&0111 != 0) == (expected[0] == "100755")))
		if actual != expected[2] || !modeMatches {
			return fmt.Errorf("CHECKPOINT_RESTORATION_FAILED: %s", name)
		}
	}
	return nil
}
