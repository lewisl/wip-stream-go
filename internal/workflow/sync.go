package workflow

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
)

type Result struct {
	OperationID       string   `json:"operationId,omitempty"`
	Checkout          string   `json:"checkout,omitempty"`
	CheckpointCreated bool     `json:"checkpointCreated,omitempty"`
	Published         bool     `json:"published,omitempty"`
	Pending           bool     `json:"pending,omitempty"`
	Conflicts         []string `json:"conflicts,omitempty"`
	Message           string   `json:"message"`
}
type Options struct {
	Message      func(string) (string, error)
	Parent       func(string) (string, error)
	Confirm      func(string) (bool, error)
	InitChoice   func(string) (string, error)
	Remote       string
	Authority    string
	BackupParent string
	Discard      bool
	Disposition  string
	OperationID  string
}
type Branch struct {
	Name, Relation          string
	Local, Previous, Remote *string
}

func tips(repo *git.Repository, prefix string) (map[string]string, error) {
	refs, e := repo.Refs(prefix)
	if e != nil {
		return nil, e
	}
	m := map[string]string{}
	for _, r := range refs {
		m[strings.TrimPrefix(r.Ref, prefix)] = r.ObjectID
	}
	return m, nil
}
func inventory(repo *git.Repository, remote string, previous map[string]string) ([]Branch, error) {
	local, e := tips(repo, "refs/heads/")
	if e != nil {
		return nil, e
	}
	fetched, e := tips(repo, git.Tracking(remote, ""))
	if e != nil {
		return nil, e
	}
	names := map[string]bool{}
	for _, m := range []map[string]string{local, fetched, previous} {
		for k := range m {
			names[k] = true
		}
	}
	keys := slices.Sorted(maps.Keys(names))
	branches := []Branch{}
	for _, name := range keys {
		b := Branch{Name: name}
		if v, ok := local[name]; ok {
			b.Local = git.Ptr(v)
		}
		if v, ok := fetched[name]; ok {
			b.Remote = git.Ptr(v)
		}
		if v, ok := previous[name]; ok {
			b.Previous = git.Ptr(v)
		}
		switch {
		case b.Local == nil && b.Remote != nil:
			b.Relation = "remote-only"
		case b.Remote == nil && b.Previous != nil:
			b.Relation = "remotely-deleted"
		case b.Local != nil && b.Remote == nil:
			b.Relation = "local-only"
		case b.Local == nil && b.Remote == nil:
			continue
		case *b.Local == *b.Remote:
			b.Relation = "equal"
		default:
			behind, e := repo.Ancestor(*b.Local, *b.Remote)
			if e != nil {
				return nil, e
			}
			ahead, e := repo.Ancestor(*b.Remote, *b.Local)
			if e != nil {
				return nil, e
			}
			switch {
			case behind:
				b.Relation = "remote-ahead"
			case ahead:
				b.Relation = "local-ahead"
			default:
				b.Relation = "diverged"
			}
		}
		branches = append(branches, b)
	}
	return branches, nil
}
func safeBranches(branches []Branch, publish bool) error {
	unsafe := []string{}
	for _, b := range branches {
		if b.Relation == "diverged" || (!publish && (b.Relation == "local-ahead" || b.Relation == "local-only")) || (b.Relation == "remotely-deleted" && b.Local != nil && !git.Same(b.Local, b.Previous)) {
			unsafe = append(unsafe, b.Name+" ("+b.Relation+")")
		}
	}
	if len(unsafe) > 0 {
		return fmt.Errorf("UNSAFE_BRANCHES: %s; local work retained; remote handoff incomplete", strings.Join(unsafe, ", "))
	}
	return nil
}
func parity(repo *git.Repository, remote string) error {
	local, e := tips(repo, "refs/heads/")
	if e != nil {
		return e
	}
	fetched, e := tips(repo, git.Tracking(remote, ""))
	if e != nil {
		return e
	}
	if !maps.Equal(local, fetched) {
		return fmt.Errorf("BRANCH_PARITY_FAILED: local and fetched remote branches differ")
	}
	return nil
}
func chooseCheckout(repo *git.Repository, branch, defaultBranch string, branches []Branch) (string, error) {
	deleted := false
	available := map[string]bool{}
	for _, b := range branches {
		if b.Remote != nil {
			available[b.Name] = true
		}
		if b.Name == branch && b.Relation == "remotely-deleted" && b.Local != nil {
			deleted = true
		}
	}
	if !deleted {
		return branch, nil
	}
	parent, e := repo.ConfigOne("branch." + branch + ".wipstreamParent")
	if e != nil {
		return "", e
	}
	if available[parent] {
		return parent, nil
	}
	if !available[defaultBranch] {
		return "", fmt.Errorf("NO_SURVIVING_CHECKOUT")
	}
	return defaultBranch, nil
}
func verifyInputs(repo *git.Repository, branch string, local, remoteTips map[string]string, remote string) error {
	b, e := repo.Branch()
	if e != nil {
		return e
	}
	status, e := repo.Status()
	if e != nil {
		return e
	}
	l, e := tips(repo, "refs/heads/")
	if e != nil {
		return e
	}
	r, e := tips(repo, git.Tracking(remote, ""))
	if e != nil {
		return e
	}
	if b != branch || status != "" || !maps.Equal(l, local) || !maps.Equal(r, remoteTips) {
		return fmt.Errorf("LOCAL_STATE_CHANGED_DURING_RECONCILIATION: inspect incomplete operation and retry")
	}
	return nil
}
func reconcile(repo *git.Repository, command, remote, current, target string, branches []Branch, checkpoint *operations.Checkpoint, config []operations.Configuration, head *operations.RemoteHead) (Result, error) {
	p := operations.NewPlan(command)
	p.Checkout = operations.Checkout{Before: current, After: target}
	p.Checkpoint = checkpoint
	p.RemoteHead = head
	if config != nil {
		p.ConfigurationChanges = config
	}
	local := map[string]string{}
	expectedRemote := map[string]string{}
	beforeRemote := map[string]string{}
	for _, b := range branches {
		if b.Local != nil {
			local[b.Name] = *b.Local
		}
		if b.Remote != nil {
			expectedRemote[b.Name] = *b.Remote
			beforeRemote[b.Name] = *b.Remote
		}
		switch b.Relation {
		case "local-ahead", "local-only":
			p.RemoteRefUpdates = append(p.RemoteRefUpdates, git.RemoteUpdate{Ref: git.Local(b.Name), Expected: b.Remote, Proposed: b.Local})
			expectedRemote[b.Name] = *b.Local
		case "remote-ahead", "remote-only":
			p.LocalRefUpdates = append(p.LocalRefUpdates, git.Update{Ref: git.Local(b.Name), ExpectedOld: b.Local, Proposed: b.Remote})
		case "remotely-deleted":
			if b.Local != nil {
				p.LocalRefUpdates = append(p.LocalRefUpdates, git.Update{Ref: git.Local(b.Name), ExpectedOld: b.Local})
				p.DestructiveEffects = append(p.DestructiveEffects, operations.Effect{Kind: "delete-local-ref", Ref: git.Local(b.Name), Description: "Remove safely proven remotely deleted branch"})
			}
		}
	}
	if current != target {
		p.DestructiveEffects = append(p.DestructiveEffects, operations.Effect{Kind: "replace-checkout", Ref: git.Local(current), Description: "Switch to " + target})
	}
	result := Result{OperationID: p.OperationID, Checkout: target, CheckpointCreated: checkpoint != nil, Published: command != "Get from Remote", Message: command + " completed"}
	e := operations.Recorded(repo, p, func(r *operations.Receipt) error {
		if e := verifyInputs(repo, current, local, beforeRemote, remote); e != nil {
			return e
		}
		if checkpoint != nil {
			if e := operations.Boundary(repo, r, "local-refs", func() error {
				return repo.UpdateRefs([]git.Update{{Ref: operations.RecoveryRef(p.OperationID, len(p.LocalRefUpdates)), Proposed: git.Ptr(checkpoint.Before)}})
			}); e != nil {
				return e
			}
		}
		if len(p.RemoteRefUpdates) > 0 {
			if e := operations.Boundary(repo, r, "remote-push", func() error { return repo.Push(remote, p.RemoteRefUpdates, false) }); e != nil {
				return e
			}
			if e := operations.Boundary(repo, r, "remote-fetch", func() error { return repo.Fetch(remote) }); e != nil {
				return e
			}
		}
		if e := verifyInputs(repo, current, local, expectedRemote, remote); e != nil {
			return e
		}
		changing := false
		for _, u := range p.LocalRefUpdates {
			if u.Ref == git.Local(current) {
				changing = true
			}
		}
		if changing {
			if e := mutation(repo, r, "checkout", "switch", "--detach"); e != nil {
				return e
			}
		}
		if len(p.LocalRefUpdates) > 0 {
			if e := operations.ApplyLocal(repo, r); e != nil {
				return e
			}
		}
		if changing || current != target {
			if e := mutation(repo, r, "checkout", "switch", target); e != nil {
				return e
			}
		}
		if e := verifyCheckout(repo, remote, target); e != nil {
			return e
		}
		if e := operations.ApplyHead(repo, r); e != nil {
			return e
		}
		if e := operations.ApplyConfig(repo, r); e != nil {
			return e
		}
		if e := verifyCheckout(repo, remote, target); e != nil {
			return e
		}
		return operations.Complete(repo, r)
	})
	if e != nil {
		result.Published = false
		result.Message = "Local checkpoint retained; remote handoff incomplete. Do not resume work in another clone."
	}
	return result, e
}
func mutation(repo *git.Repository, r *operations.Receipt, boundary string, args ...string) error {
	return operations.Boundary(repo, r, boundary, func() error { _, e := repo.Mutate(nil, args...); return e })
}
func verifyCheckout(repo *git.Repository, remote, target string) error {
	if e := parity(repo, remote); e != nil {
		return e
	}
	b, e := repo.Branch()
	if e != nil {
		return e
	}
	status, e := repo.Status()
	if e != nil {
		return e
	}
	if b != target || status != "" {
		return fmt.Errorf("RECONCILIATION_VERIFICATION_FAILED: expected clean checkout on %s", target)
	}
	return nil
}
func checkpoint(repo *git.Repository, branch string, opts Options) (*operations.Checkpoint, error) {
	before, e := repo.Hash(git.Local(branch))
	if e != nil {
		return nil, e
	}
	if _, e = repo.Mutate(nil, "add", "--all"); e != nil {
		return nil, e
	}
	_, e = repo.Run("diff", "--cached", "--quiet")
	if e == nil {
		return nil, nil
	}
	ge, ok := e.(*git.Error)
	if !ok || ge.Code != 1 {
		return nil, e
	}
	message := "WIP checkpoint " + operations.Now()
	if opts.Message != nil {
		state, e := snapshotState(repo)
		if e != nil {
			return nil, e
		}
		message, e = opts.Message(message)
		if e != nil {
			return nil, e
		}
		after, e := snapshotState(repo)
		if e != nil {
			return nil, e
		}
		if state != after {
			return nil, fmt.Errorf("WORKTREE_CHANGED_DURING_CHECKPOINT: review work and retry")
		}
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("INVALID_CHECKPOINT_MESSAGE")
	}
	if _, e = repo.Mutate(nil, "commit", "-m", message); e != nil {
		return nil, e
	}
	after, e := repo.Hash(git.Local(branch))
	if e != nil {
		return nil, e
	}
	status, e := repo.Status()
	if e != nil {
		return nil, e
	}
	if status != "" {
		return nil, fmt.Errorf("WORKTREE_CHANGED_DURING_CHECKPOINT: checkpoint retained locally")
	}
	return &operations.Checkpoint{Branch: branch, Before: before, After: after, Message: message}, nil
}
func Save(repo *git.Repository, opts Options) (result Result, err error) {
	err = locked(repo, "Commit and Save", true, func() error {
		if e := preflight(repo, false, true); e != nil {
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
		cp, e := checkpoint(repo, branch, opts)
		if e != nil {
			return e
		}
		result.CheckpointCreated = cp != nil
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
		if e = safeBranches(branches, true); e != nil {
			return e
		}
		def, e := repo.CachedDefault(remote)
		if e != nil {
			return e
		}
		target, e := chooseCheckout(repo, branch, def, branches)
		if e != nil {
			return e
		}
		p := operations.NewPlan("")
		created := []string{}
		for _, b := range branches {
			if b.Remote != nil || b.Relation == "local-only" {
				created = append(created, b.Name)
			}
		}
		if e = addTracking(repo, &p, remote, created); e != nil {
			return e
		}
		result, e = reconcile(repo, "Commit and Save", remote, branch, target, branches, cp, p.ConfigurationChanges, nil)
		return e
	})
	if err != nil {
		result.Published = false
		result.Message = "Remote handoff incomplete; any checkpoint remains local. Do not resume work in another clone."
	}
	return
}
func Get(repo *git.Repository) (result Result, err error) {
	err = locked(repo, "Get from Remote", true, func() error {
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
		def, e := repo.CachedDefault(remote)
		if e != nil {
			return e
		}
		branches, e := inventory(repo, remote, previous)
		if e != nil {
			return e
		}
		if e = safeBranches(branches, false); e != nil {
			return e
		}
		target, e := chooseCheckout(repo, branch, def, branches)
		if e != nil {
			return e
		}
		p := operations.NewPlan("")
		created := []string{}
		for _, b := range branches {
			if b.Relation == "remote-only" {
				created = append(created, b.Name)
			}
		}
		if e = addTracking(repo, &p, remote, created); e != nil {
			return e
		}
		result, e = reconcile(repo, "Get from Remote", remote, branch, target, branches, nil, p.ConfigurationChanges, nil)
		return e
	})
	return
}
func initializationConfig(repo *git.Repository, remote string, names []string) ([]operations.Configuration, error) {
	p := operations.NewPlan("")
	c, e := configChange(repo, "remote."+remote+".fetch", []string{"+refs/heads/*:refs/remotes/" + remote + "/*"})
	if e != nil {
		return nil, e
	}
	p.ConfigurationChanges = append(p.ConfigurationChanges, c)
	if e = addTracking(repo, &p, remote, names); e != nil {
		return nil, e
	}
	c, e = configChange(repo, "wipstream.remote", []string{remote})
	if e != nil {
		return nil, e
	}
	p.ConfigurationChanges = append(p.ConfigurationChanges, c)
	return p.ConfigurationChanges, nil
}
func Init(repo *git.Repository, opts Options) (result Result, err error) {
	err = locked(repo, "Initialize Repository", true, func() error {
		if e := preflight(repo, false, true); e != nil {
			return e
		}
		existing, e := repo.ConfigOne("wipstream.remote")
		if e != nil {
			return e
		}
		remote := opts.Remote
		if remote == "" {
			remote = existing
		}
		if remote == "" {
			remote = "origin"
		}
		if existing != "" && existing != remote {
			return fmt.Errorf("REMOTE_MISMATCH: initialized for %s", existing)
		}
		if strings.TrimSpace(remote) != remote {
			return fmt.Errorf("INVALID_REMOTE")
		}
		if _, e = repo.Run("remote", "get-url", remote); e != nil {
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
		def, e := repo.Default(remote)
		if e != nil {
			return e
		}
		branches, e := inventory(repo, remote, previous)
		if e != nil {
			return e
		}
		status, e := repo.Status()
		if e != nil {
			return e
		}
		choiceRequired := status != ""
		for _, b := range branches {
			if b.Relation != "equal" && b.Relation != "remote-only" {
				choiceRequired = true
			}
		}
		approved, e := snapshotState(repo)
		if e != nil {
			return e
		}
		approvedRemote, e := tips(repo, git.Tracking(remote, ""))
		if e != nil {
			return e
		}
		choice := opts.Authority
		if choice == "" && choiceRequired {
			if opts.InitChoice == nil {
				return fmt.Errorf("AUTHORITY_CHOICE_REQUIRED: select local-work, remote, reconcile, or cancel")
			}
			choice, e = opts.InitChoice("Initialization covers all ordinary branches and finishes on " + def)
			if e != nil {
				return e
			}
		}
		if choice == "" {
			choice = "local-work"
		}
		switch choice {
		case "cancel":
			return fmt.Errorf("CANCELLED: local work retained")
		case "reconcile":
			return fmt.Errorf("RECONCILIATION_REQUIRED: resolve divergent histories locally, then initialize with local-work")
		case "local-work", "remote":
		default:
			return fmt.Errorf("INVALID_AUTHORITY")
		}
		verify := func() error {
			state, e := snapshotState(repo)
			if e != nil {
				return e
			}
			if state != approved {
				return fmt.Errorf("SETUP_STATE_CHANGED: review fresh preview")
			}
			if e = repo.Fetch(remote); e != nil {
				return e
			}
			now, e := tips(repo, git.Tracking(remote, ""))
			if e != nil {
				return e
			}
			defaultNow, e := repo.Default(remote)
			if e != nil {
				return e
			}
			if !maps.Equal(now, approvedRemote) || defaultNow != def {
				return fmt.Errorf("SETUP_STATE_CHANGED: remote moved")
			}
			state, e = snapshotState(repo)
			if e != nil {
				return e
			}
			if state != approved {
				return fmt.Errorf("SETUP_STATE_CHANGED: local work moved")
			}
			return nil
		}
		if e = verify(); e != nil {
			return e
		}
		if choice == "remote" {
			result, e = adoptRemote(repo, opts, remote, branch, def, approvedRemote, verify)
			return e
		}
		cp, e := checkpoint(repo, branch, opts)
		if e != nil {
			return e
		}
		result.CheckpointCreated = cp != nil
		if e = repo.Fetch(remote); e != nil {
			return e
		}
		branches, e = inventory(repo, remote, previous)
		if e != nil {
			return e
		}
		if e = safeBranches(branches, true); e != nil {
			return e
		}
		fetched, e := tips(repo, git.Tracking(remote, ""))
		if e != nil {
			return e
		}
		defaultTip, ok := fetched[def]
		if !ok {
			return fmt.Errorf("REMOTE_DEFAULT_MISSING")
		}
		if e = repo.Push(remote, []git.RemoteUpdate{{Ref: git.Local(def), Expected: git.Ptr(defaultTip), Proposed: git.Ptr(defaultTip)}}, true); e != nil {
			return e
		}
		names := map[string]bool{}
		for _, b := range branches {
			if b.Remote != nil || b.Relation == "local-only" {
				names[b.Name] = true
			}
		}
		config, e := initializationConfig(repo, remote, slices.Sorted(maps.Keys(names)))
		if e != nil {
			return e
		}
		before, e := repo.Symbolic(git.Tracking(remote, "HEAD"))
		if e != nil {
			return e
		}
		head := &operations.RemoteHead{Remote: remote, Before: before, After: git.Ptr(git.Tracking(remote, def))}
		result, e = reconcile(repo, "Initialize Repository", remote, branch, def, branches, cp, config, head)
		return e
	})
	return
}
