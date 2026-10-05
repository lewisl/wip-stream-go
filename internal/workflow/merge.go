package workflow

import (
	"fmt"
	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
	"strings"
)

func merge(repo *git.Repository, command, branch, target, targetCommit string) (result Result, err error) {
	before, e := repo.Hash(git.Local(branch))
	if e != nil {
		return result, e
	}
	index, e := repo.Run("write-tree")
	if e != nil {
		return result, e
	}
	status, e := repo.Status()
	if e != nil {
		return result, e
	}
	p := operations.NewPlan(command)
	p.Checkout = operations.Checkout{Before: branch, After: branch}
	p.DestructiveEffects = append(p.DestructiveEffects, operations.Effect{Kind: "rewrite-local-ref", Ref: git.Local(branch), Description: "Merge " + target + " into " + branch})
	pending := &operations.PendingMerge{Kind: "merge", Command: command, Branch: branch, MergeTarget: target, MergeTargetCommit: targetCommit, PreHead: before, PreIndexTree: index, PreStatus: status, Conflicts: []string{}}
	result = Result{OperationID: p.OperationID, Checkout: branch, Message: command + " completed"}
	err = operations.Recorded(repo, p, func(r *operations.Receipt) error {
		if e := operations.Boundary(repo, r, "local-refs", func() error {
			return repo.UpdateRefs([]git.Update{{Ref: operations.RecoveryRef(p.OperationID, 0), Proposed: git.Ptr(before)}})
		}); e != nil {
			return e
		}
		e := operations.Boundary(repo, r, "merge", func() error {
			r.PendingMerge = pending
			if e := operations.Write(repo, r, false); e != nil {
				return e
			}
			_, e := repo.Mutate(nil, "merge", "--no-edit", targetCommit)
			return e
		})
		if e != nil {
			active, ae := repo.Active()
			if ae != nil {
				return ae
			}
			if _, ok := e.(*git.Error); ok && active {
				pending.Conflicts, ae = repo.Conflicts()
				if ae != nil {
					return ae
				}
				if ae = operations.Write(repo, r, false); ae != nil {
					return ae
				}
				result.Pending = true
				result.Conflicts = pending.Conflicts
				result.Message = "Merge pending; resolve conflicts and use continue or abort"
				return nil
			}
			return e
		}
		after, e := repo.Hash(git.Local(branch))
		if e != nil {
			return e
		}
		r.Outcome = &operations.Outcome{AdditionalLocalRefUpdates: []git.Update{{Ref: git.Local(branch), ExpectedOld: git.Ptr(before), Proposed: git.Ptr(after)}}}
		return operations.Complete(repo, r)
	})
	return
}
func Reconcile(repo *git.Repository, opts Options) (result Result, err error) {
	err = locked(repo, "Reconcile with Remote", true, func() error {
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
		previous, e := tips(repo, git.Tracking(remote, ""))
		if e != nil {
			return e
		}
		if e = repo.Fetch(remote); e != nil {
			return e
		}
		branches, e := inventory(repo, remote, previous)
		if e != nil {
			return e
		}
		var current *Branch
		for i := range branches {
			b := &branches[i]
			if b.Name == branch {
				current = b
			} else if b.Relation == "diverged" {
				return fmt.Errorf("OTHER_DIVERGENCE: %s", b.Name)
			}
		}
		if current == nil || current.Relation != "diverged" || current.Remote == nil {
			return fmt.Errorf("CURRENT_BRANCH_NOT_DIVERGED: run save")
		}
		result, e = merge(repo, "Reconcile with Remote", branch, git.Tracking(remote, branch), *current.Remote)
		return e
	})
	if err != nil || result.Pending {
		return
	}
	saved, e := Save(repo, opts)
	if e != nil {
		return result, e
	}
	result.Published = saved.Published
	return
}
func pendingReceipt(repo *git.Repository) (*operations.Receipt, error) {
	receipts, e := operations.List(repo)
	if e != nil {
		return nil, e
	}
	var selected *operations.Receipt
	for _, r := range receipts {
		if r.Incomplete() && r.PendingMerge != nil {
			if selected != nil {
				return nil, fmt.Errorf("MULTIPLE_PENDING_OPERATIONS")
			}
			selected = r
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("NO_PENDING_MERGE")
	}
	return selected, nil
}
func requireMerge(repo *git.Repository, p *operations.PendingMerge) error {
	branch, e := repo.Branch()
	if e != nil {
		return e
	}
	head, e := repo.Hash("HEAD")
	if e != nil {
		return e
	}
	mergeHead, e := repo.Object("MERGE_HEAD")
	if e != nil {
		return e
	}
	target := p.MergeTargetCommit
	if target == "" {
		target, e = repo.Hash(p.MergeTarget)
		if e != nil {
			return e
		}
	}
	if branch != p.Branch || head != p.PreHead || git.Value(mergeHead) != target {
		return fmt.Errorf("MERGE_STATE_MISMATCH: active merge differs from WipStream receipt")
	}
	return nil
}
func externalMerge(repo *git.Repository, r *operations.Receipt) (*operations.Recovery, error) {
	if !r.Incomplete() || r.PendingMerge == nil {
		return nil, nil
	}
	active, e := repo.Active()
	if e != nil || active {
		return nil, e
	}
	conflicts, e := repo.Conflicts()
	if e != nil || len(conflicts) > 0 {
		return nil, e
	}
	p := r.PendingMerge
	branch, e := repo.Branch()
	if e != nil {
		return nil, e
	}
	if branch != p.Branch {
		return nil, nil
	}
	head, e := repo.Hash(git.Local(branch))
	if e != nil {
		return nil, e
	}
	var resolution *operations.Recovery
	if head == p.PreHead {
		if strings.TrimSpace(p.PreStatus) != "" {
			return nil, nil
		}
		status, e := repo.Status()
		if e != nil {
			return nil, e
		}
		_, e = repo.Run("diff", "--cached", "--quiet", p.PreIndexTree, "--")
		if e == nil && status == p.PreStatus {
			resolution = &operations.Recovery{Resolution: "merge-aborted-externally", Branch: branch, Head: head}
		} else if ge, ok := e.(*git.Error); e != nil && (!ok || ge.Code != 1) {
			return nil, e
		}
	} else {
		contains, e := repo.Ancestor(p.PreHead, head)
		if e != nil {
			return nil, e
		}
		if !contains {
			return nil, nil
		}
		history, e := repo.Run("log", "--first-parent", "--format=%H %P", p.PreHead+".."+head, "--")
		if e != nil {
			return nil, e
		}
		for _, line := range strings.Split(history, "\n") {
			f := strings.Fields(line)
			if len(f) != 3 || f[1] != p.PreHead {
				continue
			}
			match := f[2] == p.MergeTargetCommit
			if p.MergeTargetCommit == "" {
				target, e := repo.Object(p.MergeTarget)
				if e != nil {
					return nil, e
				}
				if target != nil {
					match = f[2] == *target
					if !match {
						match, e = repo.Ancestor(f[0], *target)
						if e != nil {
							return nil, e
						}
					}
				}
			}
			if match {
				resolution = &operations.Recovery{Resolution: "merge-completed-externally", Branch: branch, Head: head, MergeCommit: f[0]}
				break
			}
		}
	}
	if resolution != nil {
		if e = operations.Close(repo, r, "recovered", resolution); e != nil {
			return nil, e
		}
	}
	return resolution, nil
}
func Continue(repo *git.Repository, opts Options) (result Result, err error) {
	err = locked(repo, "Continue", false, func() error {
		r, e := pendingReceipt(repo)
		if e != nil {
			return e
		}
		p := r.PendingMerge
		conflicts, e := repo.Conflicts()
		if e != nil {
			return e
		}
		if len(conflicts) > 0 {
			return fmt.Errorf("UNRESOLVED_CONFLICTS: %s", strings.Join(conflicts, ", "))
		}
		active, e := repo.Active()
		if e != nil {
			return e
		}
		if !active {
			resolution, e := externalMerge(repo, r)
			if e != nil {
				return e
			}
			if resolution == nil {
				return fmt.Errorf("MERGE_STATE_MISSING: run recover")
			}
			if resolution.Resolution != "merge-completed-externally" {
				return fmt.Errorf("MERGE_ALREADY_ABORTED: record closed; run save")
			}
		} else {
			if e = requireMerge(repo, p); e != nil {
				return e
			}
			if _, e = repo.Mutate(nil, "add", "--all"); e != nil {
				return e
			}
			if _, e = repo.Mutate(nil, "commit", "--no-edit"); e != nil {
				return e
			}
			if e = operations.Phase(repo, r, "after-merge"); e != nil {
				return e
			}
			after, e := repo.Hash(git.Local(p.Branch))
			if e != nil {
				return e
			}
			r.Outcome = &operations.Outcome{AdditionalLocalRefUpdates: []git.Update{{Ref: git.Local(p.Branch), ExpectedOld: git.Ptr(p.PreHead), Proposed: git.Ptr(after)}}}
			if e = operations.Complete(repo, r); e != nil {
				return e
			}
		}
		result = Result{OperationID: r.Plan.OperationID, Checkout: p.Branch, Message: "Merge continued"}
		return nil
	})
	if err != nil {
		return
	}
	saved, e := Save(repo, opts)
	if e != nil {
		return result, e
	}
	result.Published = saved.Published
	return
}
func Abort(repo *git.Repository) (result Result, err error) {
	err = locked(repo, "Abort", false, func() error {
		r, e := pendingReceipt(repo)
		if e != nil {
			return e
		}
		p := r.PendingMerge
		active, e := repo.Active()
		if e != nil {
			return e
		}
		if !active {
			resolution, e := externalMerge(repo, r)
			if e != nil {
				return e
			}
			if resolution == nil {
				return fmt.Errorf("MERGE_STATE_MISSING: run recover")
			}
			result = Result{OperationID: r.Plan.OperationID, Checkout: p.Branch, Message: resolution.Resolution}
			return nil
		}
		if e = requireMerge(repo, p); e != nil {
			return e
		}
		if _, e = repo.Mutate(nil, "merge", "--abort"); e != nil {
			return e
		}
		branch, e := repo.Branch()
		if e != nil {
			return e
		}
		head, e := repo.Hash("HEAD")
		if e != nil {
			return e
		}
		status, e := repo.Status()
		if e != nil {
			return e
		}
		index, e := repo.Run("write-tree")
		if e != nil {
			return e
		}
		active, e = repo.Active()
		if e != nil {
			return e
		}
		if branch != p.Branch || head != p.PreHead || status != p.PreStatus || index != p.PreIndexTree || active {
			return fmt.Errorf("ABORT_VERIFICATION_FAILED: receipt remains incomplete")
		}
		if e = operations.Close(repo, r, "aborted", nil); e != nil {
			return e
		}
		result = Result{OperationID: r.Plan.OperationID, Checkout: branch, Message: "Merge aborted; original state verified"}
		return nil
	})
	return
}
func Recover(repo *git.Repository, opts Options) (result Result, err error) {
	reclaimed, e := RecoverLock(repo)
	if e != nil {
		return result, e
	}
	err = locked(repo, "Recover Incomplete Operation", false, func() error {
		active, e := repo.Active()
		if e != nil {
			return e
		}
		if active {
			return fmt.Errorf("GIT_OPERATION_IN_PROGRESS: use continue or abort")
		}
		conflicts, e := repo.Conflicts()
		if e != nil {
			return e
		}
		if len(conflicts) > 0 {
			return fmt.Errorf("UNRESOLVED_CONFLICTS")
		}
		receipts, e := operations.List(repo)
		if e != nil {
			return e
		}
		var incomplete []*operations.Receipt
		for _, r := range receipts {
			if r.Incomplete() {
				incomplete = append(incomplete, r)
			}
		}
		if len(incomplete) == 0 {
			if reclaimed {
				result.Message = "Stale command lock archived; no incomplete operations"
				return nil
			}
			return fmt.Errorf("NO_INCOMPLETE_OPERATION")
		}
		var chosen *operations.Receipt
		if opts.OperationID != "" {
			for _, r := range incomplete {
				if r.Plan.OperationID == opts.OperationID {
					chosen = r
				}
			}
		} else if len(incomplete) == 1 {
			chosen = incomplete[0]
		}
		if chosen == nil {
			ids := []string{}
			for _, r := range incomplete {
				ids = append(ids, r.Plan.OperationID+" ("+r.Plan.Command+", "+r.Phase+")")
			}
			return fmt.Errorf("OPERATION_SELECTION_REQUIRED: %s", strings.Join(ids, "; "))
		}
		if e = confirm(opts, "Keep current files and refs and close "+chosen.Plan.OperationID+" ("+chosen.Plan.Command+", "+chosen.Phase+")?"); e != nil {
			return e
		}
		branch, e := repo.Branch()
		if e != nil {
			return e
		}
		head, e := repo.Hash("HEAD")
		if e != nil {
			return e
		}
		if e = operations.Close(repo, chosen, "recovered", &operations.Recovery{Resolution: "kept-current-state", Branch: branch, Head: head}); e != nil {
			return e
		}
		result = Result{OperationID: chosen.Plan.OperationID, Checkout: branch, Message: "Kept current files and commits; handoff is not claimed"}
		return nil
	})
	return
}
