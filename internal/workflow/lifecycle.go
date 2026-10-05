package workflow

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
)

func parent(repo *git.Repository, branch, remote string, opts Options, persist bool) (string, error) {
	recorded, e := repo.ConfigOne("branch." + branch + ".wipstreamParent")
	if e != nil {
		return "", e
	}
	selected := recorded
	if selected == "" {
		assumed, e := repo.CachedDefault(remote)
		if e != nil {
			return "", e
		}
		if opts.Parent == nil {
			return "", fmt.Errorf("PARENT_CONFIRMATION_REQUIRED: confirm parent %s", assumed)
		}
		selected, e = opts.Parent(assumed)
		if e != nil {
			return "", e
		}
		selected = strings.TrimSpace(selected)
	}
	if selected == "" || selected == branch {
		return "", fmt.Errorf("INVALID_PARENT: parent must be a different existing local branch")
	}
	if _, e = repo.Run("check-ref-format", "--branch", selected); e != nil {
		return "", e
	}
	if _, e = repo.Hash(git.Local(selected)); e != nil {
		return "", fmt.Errorf("PARENT_MISSING: %w", e)
	}
	if recorded == "" && persist {
		if e = repo.SetConfig("branch."+branch+".wipstreamParent", []string{selected}); e != nil {
			return "", e
		}
	}
	return selected, nil
}
func Start(repo *git.Repository, name string) (result Result, err error) {
	err = locked(repo, "Start Branch", true, func() error {
		if e := preflight(repo, false, false); e != nil {
			return e
		}
		remote, e := selectedRemote(repo)
		if e != nil {
			return e
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.HasPrefix(name, "-") {
			return fmt.Errorf("INVALID_BRANCH")
		}
		if _, e = repo.Run("check-ref-format", "--branch", name); e != nil {
			return fmt.Errorf("INVALID_BRANCH: %w", e)
		}
		for _, ref := range []string{git.Local(name), git.Tracking(remote, name)} {
			tip, e := repo.Object(ref)
			if e != nil {
				return e
			}
			if tip != nil {
				return fmt.Errorf("BRANCH_EXISTS: %s", name)
			}
		}
		base, e := currentBranch(repo)
		if e != nil {
			return e
		}
		tip, e := repo.Hash(git.Local(base))
		if e != nil {
			return e
		}
		p := operations.NewPlan("Start Branch")
		p.LocalRefUpdates = append(p.LocalRefUpdates, git.Update{Ref: git.Local(name), Proposed: git.Ptr(tip)})
		p.Checkout = operations.Checkout{Before: base, After: name}
		result = Result{OperationID: p.OperationID, Checkout: name, Message: "Started " + name + " from " + base}
		return operations.Recorded(repo, p, func(r *operations.Receipt) error {
			if e := mutation(repo, r, "checkout", "switch", "-c", name, tip); e != nil {
				return e
			}
			if e := operations.Boundary(repo, r, "configuration", func() error { return repo.SetConfig("branch."+name+".wipstreamParent", []string{base}) }); e != nil {
				return e
			}
			return operations.Complete(repo, r)
		})
	})
	return
}
func fetchedParity(repo *git.Repository, remote string) error {
	if e := repo.Fetch(remote); e != nil {
		return e
	}
	return parity(repo, remote)
}
func confirm(opts Options, preview string) error {
	if opts.Confirm == nil {
		return fmt.Errorf("CONFIRMATION_REQUIRED: %s", preview)
	}
	ok, e := opts.Confirm(preview)
	if e != nil {
		return e
	}
	if !ok {
		return fmt.Errorf("CANCELLED")
	}
	return nil
}
func Update(repo *git.Repository, opts Options) (result Result, err error) {
	intended, e := currentBranch(repo)
	if e != nil {
		return result, e
	}
	get, e := Get(repo)
	if e != nil {
		return result, e
	}
	if get.Checkout != intended {
		return result, fmt.Errorf("UPDATE_CHECKOUT_CHANGED")
	}
	err = locked(repo, "Update from Parent", true, func() error {
		if e := preflight(repo, true, false); e != nil {
			return e
		}
		remote, e := selectedRemote(repo)
		if e != nil {
			return e
		}
		branch, e := currentBranch(repo)
		if e != nil {
			return e
		}
		if branch != intended {
			return fmt.Errorf("UPDATE_CHECKOUT_CHANGED")
		}
		base, e := parent(repo, branch, remote, opts, true)
		if e != nil {
			return e
		}
		current, e := repo.Branch()
		if e != nil {
			return e
		}
		if current != branch {
			return fmt.Errorf("UPDATE_CHECKOUT_CHANGED")
		}
		contains, e := repo.Ancestor(git.Local(base), git.Local(branch))
		if e != nil {
			return e
		}
		if contains {
			result = Result{Checkout: branch, Message: "Already contains parent " + base}
			return nil
		}
		target, e := repo.Hash(git.Local(base))
		if e != nil {
			return e
		}
		result, e = merge(repo, "Update from Parent", branch, git.Local(base), target)
		return e
	})
	return
}
func Finish(repo *git.Repository, opts Options) (result Result, err error) {
	var branch, base, disposition string
	err = locked(repo, "Finish Branch preview", true, func() error {
		if e := preflight(repo, false, false); e != nil {
			return e
		}
		remote, e := selectedRemote(repo)
		if e != nil {
			return e
		}
		branch, e = currentBranch(repo)
		if e != nil {
			return e
		}
		def, e := repo.CachedDefault(remote)
		if e != nil {
			return e
		}
		if branch == def {
			return fmt.Errorf("DEFAULT_BRANCH: start or select a work branch")
		}
		base, e = parent(repo, branch, remote, opts, false)
		if e != nil {
			return e
		}
		contains, e := repo.Ancestor(git.Local(base), git.Local(branch))
		if e != nil {
			return e
		}
		if !contains {
			return fmt.Errorf("PARENT_UPDATE_REQUIRED: run update")
		}
		disposition = opts.Disposition
		if disposition != "retain" && disposition != "delete" {
			return fmt.Errorf("DISPOSITION_REQUIRED: choose retain or delete")
		}
		if e = confirm(opts, "Finish "+branch+" into "+base+" and "+disposition+" work branch?"); e != nil {
			return e
		}
		now, e := repo.Branch()
		if e != nil {
			return e
		}
		if now != branch {
			return fmt.Errorf("CHECKOUT_CHANGED")
		}
		return nil
	})
	if err != nil {
		return
	}
	now, e := repo.Branch()
	if e != nil {
		return result, e
	}
	if now != branch {
		return result, fmt.Errorf("CHECKOUT_CHANGED")
	}
	saved, e := Save(repo, opts)
	if e != nil {
		return result, fmt.Errorf("SAVE_HANDOFF_INCOMPLETE: %w", e)
	}
	if !saved.Published {
		return result, fmt.Errorf("SAVE_HANDOFF_INCOMPLETE")
	}
	err = locked(repo, "Finish Branch", true, func() error {
		if e := preflight(repo, true, false); e != nil {
			return e
		}
		remote, e := selectedRemote(repo)
		if e != nil {
			return e
		}
		now, e := currentBranch(repo)
		if e != nil {
			return e
		}
		recorded, e := repo.ConfigOne("branch." + branch + ".wipstreamParent")
		if e != nil {
			return e
		}
		if now != branch || (recorded != "" && recorded != base) {
			return fmt.Errorf("FINISH_TARGET_CHANGED")
		}
		if e = fetchedParity(repo, remote); e != nil {
			return e
		}
		contains, e := repo.Ancestor(git.Local(base), git.Local(branch))
		if e != nil {
			return e
		}
		if !contains {
			return fmt.Errorf("PARENT_UPDATE_REQUIRED")
		}
		baseTip, e := repo.Hash(git.Local(base))
		if e != nil {
			return e
		}
		branchTip, e := repo.Hash(git.Local(branch))
		if e != nil {
			return e
		}
		p := operations.NewPlan("Finish Branch")
		p.Checkout = operations.Checkout{Before: branch, After: base}
		p.LocalRefUpdates = append(p.LocalRefUpdates, git.Update{Ref: git.Local(base), ExpectedOld: git.Ptr(baseTip), Proposed: git.Ptr(branchTip)})
		p.RemoteRefUpdates = append(p.RemoteRefUpdates, git.RemoteUpdate{Ref: git.Local(base), Expected: git.Ptr(baseTip), Proposed: git.Ptr(branchTip)})
		p.DestructiveEffects = append(p.DestructiveEffects, operations.Effect{Kind: "replace-checkout", Description: "Switch to parent " + base})
		if disposition == "delete" {
			p.LocalRefUpdates = append(p.LocalRefUpdates, git.Update{Ref: git.Local(branch), ExpectedOld: git.Ptr(branchTip)})
			p.RemoteRefUpdates = append(p.RemoteRefUpdates, git.RemoteUpdate{Ref: git.Local(branch), Expected: git.Ptr(branchTip)})
			keys, e := repo.BranchConfigKeys(branch)
			if e != nil {
				return e
			}
			for _, key := range keys {
				c, e := configChange(repo, key, []string{})
				if e != nil {
					return e
				}
				p.ConfigurationChanges = append(p.ConfigurationChanges, c)
			}
			p.DestructiveEffects = append(p.DestructiveEffects, operations.Effect{Kind: "delete-local-ref", Ref: git.Local(branch), Description: "Delete completed branch"}, operations.Effect{Kind: "delete-remote-ref", Ref: git.Local(branch), Description: "Delete completed remote branch"})
		} else {
			c, e := configChange(repo, "branch."+branch+".wipstreamParent", []string{base})
			if e != nil {
				return e
			}
			p.ConfigurationChanges = append(p.ConfigurationChanges, c)
		}
		result = Result{OperationID: p.OperationID, Checkout: base, Published: true, Message: "Finished " + branch + " into " + base}
		return publishLifecycle(repo, p, remote)
	})
	return
}
func Condense(repo *git.Repository, opts Options) (result Result, err error) {
	err = locked(repo, "Condense Branch", true, func() error {
		if e := preflight(repo, true, false); e != nil {
			return e
		}
		remote, e := selectedRemote(repo)
		if e != nil {
			return e
		}
		branch, e := currentBranch(repo)
		if e != nil {
			return e
		}
		base, e := parent(repo, branch, remote, opts, true)
		if e != nil {
			return e
		}
		if e = fetchedParity(repo, remote); e != nil {
			return e
		}
		contains, e := repo.Ancestor(git.Local(base), git.Local(branch))
		if e != nil {
			return e
		}
		if !contains {
			return fmt.Errorf("PARENT_UPDATE_REQUIRED")
		}
		old, e := repo.Hash(git.Local(branch))
		if e != nil {
			return e
		}
		parentTip, e := repo.Hash(git.Local(base))
		if e != nil {
			return e
		}
		count, e := repo.Run("rev-list", "--count", git.Local(base)+".."+git.Local(branch))
		if e != nil {
			return e
		}
		n, e := strconv.Atoi(count)
		if e != nil {
			return e
		}
		if n < 2 {
			return fmt.Errorf("NOTHING_TO_CONDENSE")
		}
		state, e := snapshotState(repo)
		if e != nil {
			return e
		}
		if e = confirm(opts, fmt.Sprintf("Condense %s: replace %d commits after %s at %s?", branch, n, base, old)); e != nil {
			return e
		}
		message := ""
		if opts.Message != nil {
			message, e = opts.Message("Condense " + branch)
			if e != nil {
				return e
			}
		}
		message = strings.TrimSpace(message)
		if message == "" {
			return fmt.Errorf("INVALID_CHECKPOINT_MESSAGE")
		}
		after, e := snapshotState(repo)
		if e != nil {
			return e
		}
		if state != after {
			return fmt.Errorf("LOCAL_STATE_CHANGED: review fresh preview")
		}
		tree, e := repo.Run("rev-parse", old+"^{tree}")
		if e != nil {
			return e
		}
		newTip, e := repo.Mutate(nil, "commit-tree", tree, "-p", parentTip, "-m", message)
		if e != nil {
			return e
		}
		p := operations.NewPlan("Condense Branch")
		p.Checkout = operations.Checkout{Before: branch, After: branch}
		p.LocalRefUpdates = append(p.LocalRefUpdates, git.Update{Ref: git.Local(branch), ExpectedOld: git.Ptr(old), Proposed: git.Ptr(newTip)})
		p.RemoteRefUpdates = append(p.RemoteRefUpdates, git.RemoteUpdate{Ref: git.Local(branch), Expected: git.Ptr(old), Proposed: git.Ptr(newTip)})
		p.DestructiveEffects = append(p.DestructiveEffects, operations.Effect{Kind: "rewrite-local-ref", Ref: git.Local(branch), Description: "Replace checkpoints"}, operations.Effect{Kind: "rewrite-remote-ref", Ref: git.Local(branch), Description: "Replace remote checkpoints"})
		result = Result{OperationID: p.OperationID, Checkout: branch, Published: true, Message: "Condensed " + branch}
		return publishLifecycle(repo, p, remote)
	})
	return
}
func publishLifecycle(repo *git.Repository, p operations.Plan, remote string) error {
	local, e := tips(repo, "refs/heads/")
	if e != nil {
		return e
	}
	fetched, e := tips(repo, git.Tracking(remote, ""))
	if e != nil {
		return e
	}
	return operations.Recorded(repo, p, func(r *operations.Receipt) error {
		if e := verifyInputs(repo, p.Checkout.Before, local, fetched, remote); e != nil {
			return e
		}
		if e := operations.Boundary(repo, r, "remote-push", func() error { return repo.Push(remote, p.RemoteRefUpdates, false) }); e != nil {
			return e
		}
		if e := operations.Boundary(repo, r, "remote-fetch", func() error { return repo.Fetch(remote) }); e != nil {
			return e
		}
		expected := map[string]string{}
		for k, v := range fetched {
			expected[k] = v
		}
		for _, u := range p.RemoteRefUpdates {
			b := strings.TrimPrefix(u.Ref, "refs/heads/")
			if u.Proposed == nil {
				delete(expected, b)
			} else {
				expected[b] = *u.Proposed
			}
		}
		if e := verifyInputs(repo, p.Checkout.Before, local, expected, remote); e != nil {
			return e
		}
		if e := mutation(repo, r, "checkout", "switch", "--detach"); e != nil {
			return e
		}
		if e := operations.ApplyLocal(repo, r); e != nil {
			return e
		}
		if e := mutation(repo, r, "checkout", "switch", p.Checkout.After); e != nil {
			return e
		}
		if e := operations.ApplyConfig(repo, r); e != nil {
			return e
		}
		if e := verifyCheckout(repo, remote, p.Checkout.After); e != nil {
			return e
		}
		return operations.Complete(repo, r)
	})
}
