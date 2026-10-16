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
	RemoteBackup func() (parent string, discard bool, err error)
	RemoteName   string
	Authority    string
	BackupParent string
	Discard      bool
	Disposition  string
	OperationID  string
}

// Branch compares a local tip with remote-tracking tips before and after a fetch.
type Branch struct {
	Name, Relation                      string
	Tip, PreviousFetchedTip, FetchedTip *string
}

func refTips(repo *git.Repository, prefix string) (map[string]string, error) {
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
func inventory(repo *git.Repository, remoteName string, previousFetchedTips map[string]string) ([]Branch, error) {
	branchTips, e := refTips(repo, "refs/heads/")
	if e != nil {
		return nil, e
	}
	fetchedTips, e := refTips(repo, git.RemoteTrackingRef(remoteName, ""))
	if e != nil {
		return nil, e
	}
	names := map[string]bool{}
	for _, m := range []map[string]string{branchTips, fetchedTips, previousFetchedTips} {
		for k := range m {
			names[k] = true
		}
	}
	keys := slices.Sorted(maps.Keys(names))
	branches := []Branch{}
	for _, name := range keys {
		b := Branch{Name: name}
		if v, ok := branchTips[name]; ok {
			b.Tip = git.Ptr(v)
		}
		if v, ok := fetchedTips[name]; ok {
			b.FetchedTip = git.Ptr(v)
		}
		if v, ok := previousFetchedTips[name]; ok {
			b.PreviousFetchedTip = git.Ptr(v)
		}
		switch {
		case b.Tip == nil && b.FetchedTip != nil:
			b.Relation = "remote-only"
		case b.FetchedTip == nil && b.PreviousFetchedTip != nil:
			b.Relation = "remotely-deleted"
		case b.Tip != nil && b.FetchedTip == nil:
			b.Relation = "local-only"
		case b.Tip == nil && b.FetchedTip == nil:
			continue
		case *b.Tip == *b.FetchedTip:
			b.Relation = "equal"
		default:
			behind, e := repo.Ancestor(*b.Tip, *b.FetchedTip)
			if e != nil {
				return nil, e
			}
			ahead, e := repo.Ancestor(*b.FetchedTip, *b.Tip)
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
		if b.Relation == "diverged" || (!publish && (b.Relation == "local-ahead" || b.Relation == "local-only")) || (b.Relation == "remotely-deleted" && b.Tip != nil && !git.Same(b.Tip, b.PreviousFetchedTip)) {
			unsafe = append(unsafe, b.Name+" ("+b.Relation+")")
		}
	}
	if len(unsafe) > 0 {
		return fmt.Errorf("UNSAFE_BRANCHES: %s; local work retained; remote handoff incomplete", strings.Join(unsafe, ", "))
	}
	return nil
}
func verifyFetchedParity(repo *git.Repository, remoteName string) error {
	branchTips, e := refTips(repo, "refs/heads/")
	if e != nil {
		return e
	}
	fetchedTips, e := refTips(repo, git.RemoteTrackingRef(remoteName, ""))
	if e != nil {
		return e
	}
	if !maps.Equal(branchTips, fetchedTips) {
		return fmt.Errorf("BRANCH_PARITY_FAILED: local and fetched remote branches differ")
	}
	return nil
}
func chooseCheckout(repo *git.Repository, branch, remoteDefault string, branches []Branch) (string, error) {
	deleted := false
	available := map[string]bool{}
	for _, b := range branches {
		if b.FetchedTip != nil {
			available[b.Name] = true
		}
		if b.Name == branch && b.Relation == "remotely-deleted" && b.Tip != nil {
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
	if !available[remoteDefault] {
		return "", fmt.Errorf("NO_SURVIVING_CHECKOUT")
	}
	return remoteDefault, nil
}
func verifyInputs(repo *git.Repository, branch string, expectedBranchTips, expectedFetchedTips map[string]string, remoteName string) error {
	checkedOutBranch, e := repo.Branch()
	if e != nil {
		return e
	}
	status, e := repo.Status()
	if e != nil {
		return e
	}
	branchTips, e := refTips(repo, "refs/heads/")
	if e != nil {
		return e
	}
	fetchedTips, e := refTips(repo, git.RemoteTrackingRef(remoteName, ""))
	if e != nil {
		return e
	}
	if checkedOutBranch != branch || status != "" || !maps.Equal(branchTips, expectedBranchTips) || !maps.Equal(fetchedTips, expectedFetchedTips) {
		return fmt.Errorf("LOCAL_STATE_CHANGED_DURING_RECONCILIATION: inspect incomplete operation and retry")
	}
	return nil
}
func reconcile(repo *git.Repository, command, remoteName, current, target string, branches []Branch, checkpoint *operations.Checkpoint, config []operations.Configuration, head *operations.RemoteHead) (Result, error) {
	p := operations.NewPlan(command)
	p.Checkout = operations.Checkout{Before: current, After: target}
	p.Checkpoint = checkpoint
	p.RemoteHead = head
	if config != nil {
		p.ConfigurationChanges = config
	}
	branchTips := map[string]string{}
	expectedFetchedTips := map[string]string{}
	beforeFetchedTips := map[string]string{}
	for _, b := range branches {
		if b.Tip != nil {
			branchTips[b.Name] = *b.Tip
		}
		if b.FetchedTip != nil {
			expectedFetchedTips[b.Name] = *b.FetchedTip
			beforeFetchedTips[b.Name] = *b.FetchedTip
		}
		switch b.Relation {
		case "local-ahead", "local-only":
			p.RemoteRefUpdates = append(p.RemoteRefUpdates, git.RemoteUpdate{Ref: git.BranchRef(b.Name), Expected: b.FetchedTip, Proposed: b.Tip})
			expectedFetchedTips[b.Name] = *b.Tip
		case "remote-ahead", "remote-only":
			p.LocalRefUpdates = append(p.LocalRefUpdates, git.Update{Ref: git.BranchRef(b.Name), ExpectedOld: b.Tip, Proposed: b.FetchedTip})
		case "remotely-deleted":
			if b.Tip != nil {
				p.LocalRefUpdates = append(p.LocalRefUpdates, git.Update{Ref: git.BranchRef(b.Name), ExpectedOld: b.Tip})
				p.DestructiveEffects = append(p.DestructiveEffects, operations.Effect{Kind: "delete-local-ref", Ref: git.BranchRef(b.Name), Description: "Remove safely proven remotely deleted branch"})
			}
		}
	}
	if current != target {
		p.DestructiveEffects = append(p.DestructiveEffects, operations.Effect{Kind: "replace-checkout", Ref: git.BranchRef(current), Description: "Switch to " + target})
	}
	result := Result{OperationID: p.OperationID, Checkout: target, CheckpointCreated: checkpoint != nil, Published: command != "Get from Remote"}
	e := operations.Recorded(repo, p, func(r *operations.Receipt) error {
		if e := verifyInputs(repo, current, branchTips, beforeFetchedTips, remoteName); e != nil {
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
			if e := operations.Boundary(repo, r, "remote-push", func() error { return repo.Push(remoteName, p.RemoteRefUpdates, false) }); e != nil {
				return e
			}
			if e := operations.Boundary(repo, r, "remote-fetch", func() error { return repo.Fetch(remoteName) }); e != nil {
				return e
			}
		}
		if e := verifyInputs(repo, current, branchTips, expectedFetchedTips, remoteName); e != nil {
			return e
		}
		changing := false
		for _, u := range p.LocalRefUpdates {
			if u.Ref == git.BranchRef(current) {
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
		if e := verifyCheckout(repo, remoteName, target); e != nil {
			return e
		}
		if e := operations.ApplyHead(repo, r); e != nil {
			return e
		}
		if e := operations.ApplyConfig(repo, r); e != nil {
			return e
		}
		if e := verifyCheckout(repo, remoteName, target); e != nil {
			return e
		}
		return operations.Complete(repo, r)
	})
	if e != nil {
		result.Published = false
		result.Message = handoffFailure(result.CheckpointCreated)
	} else {
		result.Message = synchronizationMessage(command, remoteName, p)
	}
	return result, e
}
func mutation(repo *git.Repository, r *operations.Receipt, boundary string, args ...string) error {
	return operations.Boundary(repo, r, boundary, func() error { _, e := repo.Mutate(nil, args...); return e })
}
func verifyCheckout(repo *git.Repository, remoteName, target string) error {
	if e := verifyFetchedParity(repo, remoteName); e != nil {
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
	before, e := repo.Hash(git.BranchRef(branch))
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
	after, e := repo.Hash(git.BranchRef(branch))
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
		remoteName, e := selectedRemoteName(repo)
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
		previousFetchedTips, e := refTips(repo, git.RemoteTrackingRef(remoteName, ""))
		if e != nil {
			return e
		}
		if e = repo.Fetch(remoteName); e != nil {
			return e
		}
		branches, e := inventory(repo, remoteName, previousFetchedTips)
		if e != nil {
			return e
		}
		if e = safeBranches(branches, true); e != nil {
			return e
		}
		remoteDefault, e := repo.CachedRemoteDefault(remoteName)
		if e != nil {
			return e
		}
		target, e := chooseCheckout(repo, branch, remoteDefault, branches)
		if e != nil {
			return e
		}
		p := operations.NewPlan("")
		created := []string{}
		for _, b := range branches {
			if b.FetchedTip != nil || b.Relation == "local-only" {
				created = append(created, b.Name)
			}
		}
		if e = addTracking(repo, &p, remoteName, created); e != nil {
			return e
		}
		result, e = reconcile(repo, "Commit and Save", remoteName, branch, target, branches, cp, p.ConfigurationChanges, nil)
		return e
	})
	if err != nil {
		result.Published = false
		result.Message = handoffFailure(result.CheckpointCreated)
	}
	return
}
func Get(repo *git.Repository) (result Result, err error) {
	err = locked(repo, "Get from Remote", true, func() error {
		if e := preflight(repo, true, false); e != nil {
			return e
		}
		remoteName, e := selectedRemoteName(repo)
		if e != nil {
			return e
		}
		branch, e := currentBranch(repo)
		if e != nil {
			return e
		}
		previousFetchedTips, e := refTips(repo, git.RemoteTrackingRef(remoteName, ""))
		if e != nil {
			return e
		}
		if e = repo.Fetch(remoteName); e != nil {
			return e
		}
		remoteDefault, e := repo.CachedRemoteDefault(remoteName)
		if e != nil {
			return e
		}
		branches, e := inventory(repo, remoteName, previousFetchedTips)
		if e != nil {
			return e
		}
		if e = safeBranches(branches, false); e != nil {
			return e
		}
		target, e := chooseCheckout(repo, branch, remoteDefault, branches)
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
		if e = addTracking(repo, &p, remoteName, created); e != nil {
			return e
		}
		result, e = reconcile(repo, "Get from Remote", remoteName, branch, target, branches, nil, p.ConfigurationChanges, nil)
		return e
	})
	return
}
func initializationConfig(repo *git.Repository, remoteName string, names []string) ([]operations.Configuration, error) {
	p := operations.NewPlan("")
	c, e := configChange(repo, "remote."+remoteName+".fetch", []string{"+refs/heads/*:refs/remotes/" + remoteName + "/*"})
	if e != nil {
		return nil, e
	}
	p.ConfigurationChanges = append(p.ConfigurationChanges, c)
	if e = addTracking(repo, &p, remoteName, names); e != nil {
		return nil, e
	}
	c, e = configChange(repo, "wipstream.remote", []string{remoteName})
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
		configuredRemoteName, e := repo.ConfigOne("wipstream.remote")
		if e != nil {
			return e
		}
		remoteName := opts.RemoteName
		if remoteName == "" {
			remoteName = configuredRemoteName
		}
		if remoteName == "" {
			remoteName = "origin"
		}
		if configuredRemoteName != "" && configuredRemoteName != remoteName {
			return fmt.Errorf("REMOTE_MISMATCH: initialized for %s", configuredRemoteName)
		}
		if strings.TrimSpace(remoteName) != remoteName {
			return fmt.Errorf("INVALID_REMOTE")
		}
		if _, e = repo.Run("remote", "get-url", remoteName); e != nil {
			return e
		}
		branch, e := currentBranch(repo)
		if e != nil {
			return e
		}
		previousFetchedTips, e := refTips(repo, git.RemoteTrackingRef(remoteName, ""))
		if e != nil {
			return e
		}
		if e = repo.Fetch(remoteName); e != nil {
			return e
		}
		remoteDefault, e := repo.QueryRemoteDefault(remoteName)
		if e != nil {
			return e
		}
		branches, e := inventory(repo, remoteName, previousFetchedTips)
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
		approvedFetchedTips, e := refTips(repo, git.RemoteTrackingRef(remoteName, ""))
		if e != nil {
			return e
		}
		choice := opts.Authority
		if choice == "" && choiceRequired {
			if opts.InitChoice == nil {
				return fmt.Errorf("AUTHORITY_CHOICE_REQUIRED: select local-work, remote, reconcile, or cancel")
			}
			choice, e = opts.InitChoice("Initialization covers all ordinary branches and finishes on " + remoteDefault)
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
			if e = repo.Fetch(remoteName); e != nil {
				return e
			}
			freshFetchedTips, e := refTips(repo, git.RemoteTrackingRef(remoteName, ""))
			if e != nil {
				return e
			}
			queriedRemoteDefault, e := repo.QueryRemoteDefault(remoteName)
			if e != nil {
				return e
			}
			if !maps.Equal(freshFetchedTips, approvedFetchedTips) || queriedRemoteDefault != remoteDefault {
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
			result, e = adoptRemote(repo, opts, remoteName, branch, remoteDefault, approvedFetchedTips, verify)
			return e
		}
		cp, e := checkpoint(repo, branch, opts)
		if e != nil {
			return e
		}
		result.CheckpointCreated = cp != nil
		if e = repo.Fetch(remoteName); e != nil {
			return e
		}
		branches, e = inventory(repo, remoteName, previousFetchedTips)
		if e != nil {
			return e
		}
		if e = safeBranches(branches, true); e != nil {
			return e
		}
		fetchedTips, e := refTips(repo, git.RemoteTrackingRef(remoteName, ""))
		if e != nil {
			return e
		}
		fetchedDefaultTip, ok := fetchedTips[remoteDefault]
		if !ok {
			return fmt.Errorf("REMOTE_DEFAULT_MISSING")
		}
		if e = repo.Push(remoteName, []git.RemoteUpdate{{Ref: git.BranchRef(remoteDefault), Expected: git.Ptr(fetchedDefaultTip), Proposed: git.Ptr(fetchedDefaultTip)}}, true); e != nil {
			return e
		}
		names := map[string]bool{}
		for _, b := range branches {
			if b.FetchedTip != nil || b.Relation == "local-only" {
				names[b.Name] = true
			}
		}
		config, e := initializationConfig(repo, remoteName, slices.Sorted(maps.Keys(names)))
		if e != nil {
			return e
		}
		before, e := repo.Symbolic(git.RemoteTrackingRef(remoteName, "HEAD"))
		if e != nil {
			return e
		}
		head := &operations.RemoteHead{Remote: remoteName, Before: before, After: git.Ptr(git.RemoteTrackingRef(remoteName, remoteDefault))}
		result, e = reconcile(repo, "Initialize Repository", remoteName, branch, remoteDefault, branches, cp, config, head)
		return e
	})
	if err != nil && result.CheckpointCreated {
		result.Published = false
		result.Message = "Initialization did not complete. " + handoffFailure(true)
	}
	return
}
