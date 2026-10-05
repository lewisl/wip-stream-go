package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
)

type entry struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Mode     uint32 `json:"mode"`
	Contents string `json:"contents,omitempty"`
}

func digestJSON(value any) (string, error) {
	b, e := json.Marshal(value)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func permissions(info os.FileInfo) uint32 {
	m := uint32(info.Mode().Perm())
	if info.Mode()&os.ModeSetuid != 0 {
		m |= 04000
	}
	if info.Mode()&os.ModeSetgid != 0 {
		m |= 02000
	}
	if info.Mode()&os.ModeSticky != 0 {
		m |= 01000
	}
	return m
}
func fileMode(bits uint32) os.FileMode {
	m := os.FileMode(bits & 0777)
	if bits&04000 != 0 {
		m |= os.ModeSetuid
	}
	if bits&02000 != 0 {
		m |= os.ModeSetgid
	}
	if bits&01000 != 0 {
		m |= os.ModeSticky
	}
	return m
}
func snapshot(root string, excluded map[string]bool) ([]entry, string, error) {
	rooted, e := os.OpenRoot(root)
	if e != nil {
		return nil, "", e
	}
	defer rooted.Close()
	entries := []entry{}
	var inspect func(string) error
	inspect = func(name string) error {
		if excluded[name] {
			return nil
		}
		p := filepath.Join(root, filepath.FromSlash(name))
		before, e := os.Lstat(p)
		if e != nil {
			return e
		}
		en := entry{Name: name, Mode: permissions(before)}
		switch {
		case before.Mode()&os.ModeSymlink != 0:
			en.Kind = "link"
			en.Contents, e = os.Readlink(p)
		case before.Mode().IsRegular():
			en.Kind = "file"
			f, err := rooted.Open(filepath.FromSlash(name))
			if err != nil {
				return err
			}
			identity, err := f.Stat()
			if err != nil {
				f.Close()
				return err
			}
			if !os.SameFile(before, identity) {
				f.Close()
				return fmt.Errorf("PROJECT_CHANGED: %s", name)
			}
			h := sha256.New()
			_, err = io.Copy(h, f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			en.Contents = hex.EncodeToString(h.Sum(nil))
		case before.IsDir():
			en.Kind = "directory"
		default:
			return fmt.Errorf("UNSUPPORTED_PROJECT_FILE: %s", name)
		}
		if e != nil {
			return e
		}
		entries = append(entries, en)
		if before.IsDir() {
			children, e := os.ReadDir(p)
			if e != nil {
				return e
			}
			names := []string{}
			for _, child := range children {
				names = append(names, child.Name())
				if e = inspect(filepath.ToSlash(filepath.Join(name, child.Name()))); e != nil {
					return e
				}
			}
			after, e := os.ReadDir(p)
			if e != nil {
				return e
			}
			afterNames := []string{}
			for _, child := range after {
				afterNames = append(afterNames, child.Name())
			}
			if !slices.Equal(names, afterNames) {
				return fmt.Errorf("PROJECT_CHANGED: %s", name)
			}
		}
		after, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
			return fmt.Errorf("PROJECT_CHANGED: %s", name)
		}
		return nil
	}
	if e = inspect(""); e != nil {
		return nil, "", e
	}
	fingerprint, e := digestJSON(entries)
	return entries, fingerprint, e
}
func snapshotState(repo *git.Repository) (string, error) {
	// Include file bytes, modes, index flags, checkout, refs, and local configuration.
	// Ignored files are also included so a destructive approval never outlives them.
	_, files, e := snapshot(repo.Root, map[string]bool{".git": true})
	if e != nil {
		return "", e
	}
	branch, e := repo.Branch()
	if e != nil {
		return "", e
	}
	head, e := repo.Hash("HEAD")
	if e != nil {
		return "", e
	}
	refs, e := repo.Refs("refs/heads/")
	if e != nil {
		return "", e
	}
	index, e := indexSignature(repo)
	if e != nil {
		return "", e
	}
	config, e := repo.Raw(nil, "config", "--local", "--null", "--list")
	if e != nil {
		return "", e
	}
	return digestJSON([]any{branch, head, refs, files, index, string(config)})
}

var indexFlags = regexp.MustCompile("\\tflags: ([0-9a-f]+)\\n$")

func indexSignature(repo *git.Repository) (string, error) {
	raw, err := repo.Raw(nil, "ls-files", "--stage", "-v", "--debug", "-z")
	if err != nil {
		return "", err
	}
	remaining := string(raw)
	type indexEntry struct {
		Entry string
		Flags uint64
	}
	entries := []indexEntry{}
	for remaining != "" {
		separator := strings.IndexByte(remaining, 0)
		if separator < 0 {
			return "", fmt.Errorf("INDEX_INSPECTION_FAILED: incomplete entry")
		}
		entry := remaining[:separator]
		remaining = remaining[separator+1:]
		debugEnd := 0
		for line := 0; line < 5; line++ {
			newline := strings.IndexByte(remaining[debugEnd:], '\n')
			if newline < 0 {
				return "", fmt.Errorf("INDEX_INSPECTION_FAILED: incomplete flags")
			}
			debugEnd += newline + 1
		}
		match := indexFlags.FindStringSubmatch(remaining[:debugEnd])
		if match == nil {
			return "", fmt.Errorf("INDEX_INSPECTION_FAILED: unrecognized flags")
		}
		flags, err := strconv.ParseUint(match[1], 16, 32)
		if err != nil {
			return "", err
		}
		// Match the reference: retain assume-unchanged, intent-to-add, and
		// skip-worktree, while discarding incidental index stat-cache flags.
		entries = append(entries, indexEntry{entry, flags & 0x60008000})
		remaining = remaining[debugEnd:]
	}
	return digestJSON(entries)
}
func within(root, target string) bool {
	rel, e := filepath.Rel(root, target)
	return e == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func backup(repo *git.Repository, parent string) (string, error) {
	source, e := filepath.EvalSymlinks(repo.Root)
	if e != nil {
		return "", e
	}
	destParent, e := filepath.EvalSymlinks(parent)
	if e != nil {
		return "", e
	}
	if within(source, destParent) {
		return "", fmt.Errorf("INVALID_BACKUP_DESTINATION: choose a parent outside project")
	}
	ordinary := filepath.Join(source, ".git")
	info, e := os.Lstat(ordinary)
	if e != nil {
		return "", e
	}
	common, e := repo.CommonDir()
	if e != nil {
		return "", e
	}
	common, e = filepath.EvalSymlinks(common)
	if e != nil {
		return "", e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || common != ordinary {
		return "", fmt.Errorf("EXTERNAL_GIT_STORAGE: backup needs ordinary self-contained clone")
	}
	raw, e := repo.Raw(nil, "config", "--local", "--get-regexp", "^remote\\..*\\.promisor$")
	if e == nil && strings.Contains(string(raw), "true") {
		return "", fmt.Errorf("EXTERNAL_GIT_STORAGE: partial clone cannot be backed up completely")
	}
	excluded := map[string]bool{".git/wipstream/command.lock": true}
	entries, before, e := snapshot(source, excluded)
	if e != nil {
		return "", e
	}
	for _, en := range entries {
		if en.Kind == "file" && strings.HasSuffix(en.Name, "/objects/info/alternates") {
			b, e := os.ReadFile(filepath.Join(source, en.Name))
			if e != nil {
				return "", e
			}
			if strings.TrimSpace(string(b)) != "" {
				return "", fmt.Errorf("EXTERNAL_GIT_STORAGE: object alternates")
			}
		}
		if en.Kind == "link" && (en.Name == ".git" || strings.Contains(en.Name, "/.git/") || strings.HasPrefix(en.Name, ".git/")) {
			if filepath.IsAbs(en.Contents) || !within(source, filepath.Clean(filepath.Join(source, filepath.Dir(en.Name), en.Contents))) {
				return "", fmt.Errorf("EXTERNAL_GIT_STORAGE: external Git symlink")
			}
		}
		if en.Kind == "file" && strings.HasSuffix(en.Name, "/.git") {
			return "", fmt.Errorf("EXTERNAL_GIT_STORAGE: nested gitfile needs manual backup")
		}
	}
	destination := filepath.Join(destParent, filepath.Base(source)+"-backup-"+operations.ID())
	if e = os.Mkdir(destination, 0700); e != nil {
		return "", e
	}
	failed := func(err error) (string, error) {
		return destination, fmt.Errorf("%w; incomplete backup retained at %s", err, destination)
	}
	root, e := os.OpenRoot(source)
	if e != nil {
		return failed(e)
	}
	defer root.Close()
	for _, en := range entries {
		if en.Name == "" {
			continue
		}
		to := filepath.Join(destination, filepath.FromSlash(en.Name))
		switch en.Kind {
		case "directory":
			e = os.Mkdir(to, 0700)
		case "link":
			e = os.Symlink(en.Contents, to)
		case "file":
			f, err := root.Open(filepath.FromSlash(en.Name))
			if err != nil {
				return failed(err)
			}
			out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				f.Close()
				return failed(err)
			}
			_, e = io.Copy(out, f)
			if e == nil {
				e = out.Chmod(fileMode(en.Mode))
			}
			if e == nil {
				e = out.Sync()
			}
			closeErr := out.Close()
			f.Close()
			if e == nil {
				e = closeErr
			}
		}
		if e != nil {
			return failed(e)
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		en := entries[i]
		if en.Kind == "directory" {
			if e = os.Chmod(filepath.Join(destination, en.Name), fileMode(en.Mode)); e != nil {
				return failed(e)
			}
		}
	}
	_, copied, e := snapshot(destination, nil)
	if e != nil {
		return failed(e)
	}
	_, after, e := snapshot(source, excluded)
	if e != nil {
		return failed(e)
	}
	if before != after || before != copied {
		return failed(fmt.Errorf("BACKUP_VERIFICATION_FAILED: source or copied contents changed"))
	}
	return destination, nil
}
