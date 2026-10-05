package workflow

import (
	"encoding/json"
	"fmt"
	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func adoptRemote(repo *git.Repository, opts Options, remote, branch, def string, remoteTips map[string]string, revalidate func() error) (result Result, err error) {
	local, e := tips(repo, "refs/heads/")
	if e != nil {
		return result, e
	}
	defaultTip, ok := remoteTips[def]
	if !ok {
		return result, fmt.Errorf("REMOTE_DEFAULT_MISSING")
	}
	target, e := repo.Raw(nil, "ls-tree", "-r", "-z", "--full-tree", defaultTip)
	if e != nil {
		return result, e
	}
	current, e := repo.Raw(nil, "ls-tree", "-r", "-z", "--full-tree", "HEAD")
	if e != nil {
		return result, e
	}
	targetNames := []string{}
	tracked := map[string]bool{}
	for _, b := range [][]byte{target, current} {
		for _, line := range git.NulList(b) {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) != 2 {
				return result, fmt.Errorf("invalid tree output")
			}
			if strings.HasPrefix(parts[0], "160000 ") {
				return result, fmt.Errorf("REMOTE_ADOPTION_SUBMODULES")
			}
			if string(b) == string(target) {
				targetNames = append(targetNames, parts[1])
			} else {
				tracked[parts[1]] = true
			}
		}
	}
	trackedRaw, e := repo.Raw(nil, "ls-files", "--cached", "-z")
	if e != nil {
		return result, e
	}
	for _, name := range git.NulList(trackedRaw) {
		tracked[name] = true
	}
	backupInfo := map[string]string{"kind": "explicit-discard"}
	if opts.BackupParent != "" {
		p, e := backup(repo, opts.BackupParent)
		if e != nil {
			return result, e
		}
		backupInfo = map[string]string{"kind": "verified-copy", "path": p}
	} else if !opts.Discard {
		return result, fmt.Errorf("REMOTE_DISCARD_NOT_CONFIRMED: select backup parent or explicitly approve discard")
	}
	entries, fingerprint, e := snapshot(repo.Root, map[string]bool{".git": true})
	if e != nil {
		return result, e
	}
	trackedDirs := map[string]bool{}
	for name := range tracked {
		for p := filepath.ToSlash(filepath.Dir(name)); p != "." && p != ""; p = filepath.ToSlash(filepath.Dir(p)) {
			trackedDirs[p] = true
		}
	}
	untracked := []entry{}
	names := []string{}
	for _, en := range entries {
		if en.Name != "" && !tracked[en.Name] && !(en.Kind == "directory" && trackedDirs[en.Name]) {
			untracked = append(untracked, en)
			names = append(names, en.Name)
		}
	}
	ignored := map[string]bool{}
	if len(names) > 0 {
		b, e := repo.Raw([]byte(strings.Join(names, "\x00")+"\x00"), "check-ignore", "--no-index", "--stdin", "-z")
		if ge, ok := e.(*git.Error); e != nil && (!ok || ge.Code != 1) {
			return result, e
		}
		for _, name := range git.NulList(b) {
			ignored[name] = true
		}
	}
	ignorecase, e := repo.ConfigOne("core.ignorecase")
	if e != nil {
		return result, e
	}
	normalize := func(s string) string {
		if ignorecase == "true" {
			return strings.ToLower(s)
		}
		return s
	}
	for name := range ignored {
		for _, target := range targetNames {
			a, b := normalize(name), normalize(target)
			if a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") {
				return result, fmt.Errorf("IGNORED_PATH_COLLISION: %s", target)
			}
		}
	}
	removedFiles, removedDirs := []string{}, []string{}
	protected := []entry{}
	for _, en := range entries {
		if ignored[en.Name] {
			protected = append(protected, en)
		}
	}
	for _, en := range untracked {
		if !ignored[en.Name] {
			if en.Kind == "directory" {
				removedDirs = append(removedDirs, en.Name)
			} else {
				removedFiles = append(removedFiles, en.Name)
			}
		}
	}
	all := map[string]bool{}
	for k := range local {
		all[k] = true
	}
	for k := range remoteTips {
		all[k] = true
	}
	p := operations.NewPlan("Adopt Remote for Setup")
	p.Checkout = operations.Checkout{Before: branch, After: def}
	fetched := []map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(remoteTips)) {
		fetched = append(fetched, map[string]string{"name": name, "tip": remoteTips[name]})
	}
	for _, name := range slices.Sorted(maps.Keys(all)) {
		var old, new *string
		if v, ok := local[name]; ok {
			old = git.Ptr(v)
		}
		if v, ok := remoteTips[name]; ok {
			new = git.Ptr(v)
		}
		p.LocalRefUpdates = append(p.LocalRefUpdates, git.Update{Ref: git.Local(name), ExpectedOld: old, Proposed: new})
		if old != nil && !git.Same(old, new) {
			kind := "rewrite-local-ref"
			if new == nil {
				kind = "delete-local-ref"
			}
			p.DestructiveEffects = append(p.DestructiveEffects, operations.Effect{Kind: kind, Ref: git.Local(name), Description: "Adopt approved remote history"})
		}
	}
	for _, name := range targetNames {
		tracked[name] = true
	}
	p.RemoteAdoption, e = json.Marshal(map[string]any{"remote": remote, "remoteDefaultBranch": def, "fetchedTips": fetched, "worktreeFingerprint": fingerprint, "trackedPaths": slices.Sorted(maps.Keys(tracked)), "removedUntrackedPaths": append(append([]string{}, removedFiles...), removedDirs...), "preservedIgnoredPaths": slices.Sorted(maps.Keys(ignored)), "backup": backupInfo})
	if e != nil {
		return result, e
	}
	p.DestructiveEffects = append(p.DestructiveEffects, operations.Effect{Kind: "replace-files", Description: "Replace tracked files and approved non-ignored untracked entries"})
	p.ConfigurationChanges, e = initializationConfig(repo, remote, slices.Sorted(maps.Keys(remoteTips)))
	if e != nil {
		return result, e
	}
	for name := range local {
		if !all[name] {
			continue
		}
		if _, ok := remoteTips[name]; !ok {
			keys, e := repo.BranchConfigKeys(name)
			if e != nil {
				return result, e
			}
			for _, key := range keys {
				c, e := configChange(repo, key, []string{})
				if e != nil {
					return result, e
				}
				p.ConfigurationChanges = append(p.ConfigurationChanges, c)
			}
		}
	}
	before, e := repo.Symbolic(git.Tracking(remote, "HEAD"))
	if e != nil {
		return result, e
	}
	p.RemoteHead = &operations.RemoteHead{Remote: remote, Before: before, After: git.Ptr(git.Tracking(remote, def))}
	if e = revalidate(); e != nil {
		return result, e
	}
	_, now, e := snapshot(repo.Root, map[string]bool{".git": true})
	if e != nil {
		return result, e
	}
	if now != fingerprint {
		return result, fmt.Errorf("SETUP_STATE_CHANGED")
	}
	result = Result{OperationID: p.OperationID, Checkout: def, Message: "Adopted approved remote state"}
	if backupInfo["path"] != "" {
		result.Message += "; verified backup at " + backupInfo["path"]
	}
	err = operations.Recorded(repo, p, func(r *operations.Receipt) error {
		if e := revalidate(); e != nil {
			return e
		}
		if e := operations.Boundary(repo, r, "file-replacement", func() error {
			_, now, e := snapshot(repo.Root, map[string]bool{".git": true})
			if e != nil {
				return e
			}
			if now != fingerprint {
				return fmt.Errorf("SETUP_STATE_CHANGED")
			}
			remove := func(name string, directory bool) error {
				path := filepath.Join(repo.Root, filepath.FromSlash(name))
				parent, e := filepath.EvalSymlinks(filepath.Dir(path))
				if e != nil {
					return e
				}
				if !within(repo.Root, parent) {
					return fmt.Errorf("REPLACEMENT_PATH_CHANGED")
				}
				info, e := os.Lstat(path)
				if e != nil {
					return e
				}
				if info.IsDir() != directory {
					return fmt.Errorf("REPLACEMENT_PATH_CHANGED: %s", name)
				}
				return os.Remove(path)
			}
			for _, name := range removedFiles {
				if e = remove(name, false); e != nil {
					return e
				}
			}
			slices.SortFunc(removedDirs, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
			for _, name := range removedDirs {
				if e = remove(name, true); e != nil && !os.IsNotExist(e) {
					info, se := os.ReadDir(filepath.Join(repo.Root, name))
					if se != nil || len(info) == 0 {
						return e
					}
				}
			}
			_, e = repo.Mutate(nil, "switch", "--detach", "--discard-changes", defaultTip)
			return e
		}); e != nil {
			return e
		}
		if e := operations.ApplyLocal(repo, r); e != nil {
			return e
		}
		if e := mutation(repo, r, "checkout", "switch", def); e != nil {
			return e
		}
		if e := operations.Boundary(repo, r, "remote-fetch", func() error { return repo.Fetch(remote) }); e != nil {
			return e
		}
		fresh, e := tips(repo, git.Tracking(remote, ""))
		if e != nil {
			return e
		}
		freshDef, e := repo.Default(remote)
		if e != nil {
			return e
		}
		if !maps.Equal(fresh, remoteTips) || freshDef != def {
			return fmt.Errorf("REMOTE_CHANGED_DURING_ADOPTION")
		}
		if e = verifyCheckout(repo, remote, def); e != nil {
			return e
		}
		after, _, e := snapshot(repo.Root, map[string]bool{".git": true})
		if e != nil {
			return e
		}
		retained := []entry{}
		for _, en := range after {
			if ignored[en.Name] {
				retained = append(retained, en)
			}
		}
		if !slices.Equal(protected, retained) {
			return fmt.Errorf("IGNORED_FILES_CHANGED: inspect incomplete operation")
		}
		if e = operations.ApplyHead(repo, r); e != nil {
			return e
		}
		if e = operations.ApplyConfig(repo, r); e != nil {
			return e
		}
		if e = verifyCheckout(repo, remote, def); e != nil {
			return e
		}
		return operations.Complete(repo, r)
	})
	return
}
