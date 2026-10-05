// Package git delegates repository semantics to the installed Git executable.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type Repository struct {
	Root    string
	Context context.Context
}
type Error struct {
	Args   []string
	Code   int
	Output string
}

func (e *Error) Error() string { return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), e.Output) }

type Ref struct {
	Ref      string `json:"ref"`
	ObjectID string `json:"objectId"`
}
type Update struct {
	Ref         string  `json:"ref"`
	ExpectedOld *string `json:"expectedOld"`
	Proposed    *string `json:"proposed"`
}
type RemoteUpdate struct {
	Ref      string  `json:"ref"`
	Expected *string `json:"expected"`
	Proposed *string `json:"proposed"`
}

func Ptr(s string) *string { return &s }
func Value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func Same(a, b *string) bool                { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
func Local(branch string) string            { return "refs/heads/" + branch }
func Tracking(remote, branch string) string { return "refs/remotes/" + remote + "/" + branch }
func Open(ctx context.Context, dir string) (*Repository, error) {
	r := &Repository{Root: dir, Context: ctx}
	root, err := r.Run("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	r.Root, err = filepath.EvalSymlinks(root)
	return r, err
}
func (r *Repository) Raw(input []byte, args ...string) ([]byte, error) {
	ctx := r.Context
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Root
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		return out.Bytes(), &Error{args, code, strings.TrimSpace(stderr.String() + "\n" + out.String())}
	}
	return out.Bytes(), nil
}
func (r *Repository) Run(args ...string) (string, error) {
	b, e := r.Raw(nil, args...)
	return strings.TrimSpace(string(b)), e
}
func (r *Repository) Mutate(input []byte, args ...string) (string, error) {
	if e := r.SingleWorktree(); e != nil {
		return "", e
	}
	b, e := r.Raw(input, args...)
	return strings.TrimSpace(string(b)), e
}
func (r *Repository) SingleWorktree() error {
	b, e := r.Raw(nil, "worktree", "list", "--porcelain", "-z")
	if e != nil {
		return e
	}
	var paths []string
	for _, f := range strings.Split(string(b), "\x00") {
		if strings.HasPrefix(f, "worktree ") {
			paths = append(paths, strings.TrimPrefix(f, "worktree "))
		}
	}
	if len(paths) != 1 {
		return fmt.Errorf("ADDITIONAL_WORKTREES: WipStream requires exactly one worktree")
	}
	p, e := filepath.EvalSymlinks(paths[0])
	if e != nil {
		return e
	}
	if p != r.Root {
		return fmt.Errorf("ADDITIONAL_WORKTREES: checkout is not the sole worktree")
	}
	return nil
}
func (r *Repository) CommonDir() (string, error) {
	p, e := r.Run("rev-parse", "--git-common-dir")
	if e != nil {
		return "", e
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.Root, p)
	}
	return p, nil
}
func (r *Repository) Branch() (string, error) {
	s, e := r.Run("symbolic-ref", "--quiet", "--short", "HEAD")
	var ge *Error
	if errors.As(e, &ge) && ge.Code == 1 {
		return "", nil
	}
	return s, e
}
func (r *Repository) Hash(ref string) (string, error) {
	return r.Run("rev-parse", "--verify", ref+"^{commit}")
}
func (r *Repository) Object(ref string) (*string, error) {
	s, e := r.Run("rev-parse", "--verify", "--quiet", ref+"^{object}")
	var ge *Error
	if errors.As(e, &ge) && ge.Code == 1 {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return Ptr(s), nil
}
func (r *Repository) Symbolic(ref string) (*string, error) {
	s, e := r.Run("symbolic-ref", "--quiet", ref)
	var ge *Error
	if errors.As(e, &ge) && ge.Code == 1 {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return Ptr(s), nil
}
func (r *Repository) Refs(prefix string) ([]Ref, error) {
	s, e := r.Run("for-each-ref", "--format=%(refname)\t%(objectname)", prefix)
	if e != nil {
		return nil, e
	}
	refs := []Ref{}
	if s != "" {
		for _, line := range strings.Split(s, "\n") {
			f := strings.Split(line, "\t")
			if len(f) != 2 {
				return nil, fmt.Errorf("invalid Git ref output")
			}
			if strings.HasSuffix(f[0], "/HEAD") {
				continue
			}
			refs = append(refs, Ref{f[0], f[1]})
		}
	}
	return refs, nil
}
func (r *Repository) Config(key string) ([]string, error) {
	b, e := r.Raw(nil, "config", "--local", "--null", "--get-all", key)
	var ge *Error
	if errors.As(e, &ge) && ge.Code == 1 {
		return []string{}, nil
	}
	if e != nil {
		return nil, e
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00"), nil
}
func (r *Repository) ConfigOne(key string) (string, error) {
	v, e := r.Config(key)
	if e != nil {
		return "", e
	}
	if len(v) == 0 {
		return "", nil
	}
	if len(v) != 1 {
		return "", fmt.Errorf("INVALID_CONFIGURATION: multiple values for %s", key)
	}
	return v[0], nil
}
func (r *Repository) SetConfig(key string, values []string) error {
	_, e := r.Mutate(nil, "config", "--local", "--unset-all", key)
	var ge *Error
	if e != nil && (!errors.As(e, &ge) || ge.Code != 5) {
		return e
	}
	for _, v := range values {
		if _, e = r.Mutate(nil, "config", "--local", "--add", key, v); e != nil {
			return e
		}
	}
	return nil
}
func (r *Repository) Status() (string, error) {
	b, e := r.Raw(nil, "--no-optional-locks", "status", "--porcelain=v1")
	return string(b), e
}
func (r *Repository) Conflicts() ([]string, error) {
	b, e := r.Raw(nil, "diff", "--name-only", "--diff-filter=U", "-z")
	return NulList(b), e
}
func NulList(b []byte) []string {
	if len(b) == 0 {
		return []string{}
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
}
func (r *Repository) Active() (bool, error) {
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
		p, e := r.Run("rev-parse", "--git-path", marker)
		if e != nil {
			return false, e
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(r.Root, p)
		}
		_, e = os.Stat(p)
		if e == nil {
			return true, nil
		}
		if !os.IsNotExist(e) {
			return false, e
		}
	}
	return false, nil
}
func (r *Repository) Ancestor(a, b string) (bool, error) {
	_, e := r.Run("merge-base", "--is-ancestor", a, b)
	var ge *Error
	if errors.As(e, &ge) && ge.Code == 1 {
		return false, nil
	}
	return e == nil, e
}
func (r *Repository) Fetch(remote string) error {
	_, e := r.Mutate(nil, "fetch", "--prune", remote, "+refs/heads/*:refs/remotes/"+remote+"/*")
	return e
}
func (r *Repository) Default(remote string) (string, error) {
	s, e := r.Run("ls-remote", "--symref", remote, "HEAD")
	if e != nil {
		return "", e
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "ref: refs/heads/") && strings.HasSuffix(line, "\tHEAD") {
			return strings.TrimSuffix(strings.TrimPrefix(line, "ref: refs/heads/"), "\tHEAD"), nil
		}
	}
	return "", fmt.Errorf("REMOTE_HEAD_MISSING: remote %s has no ordinary default branch", remote)
}
func (r *Repository) CachedDefault(remote string) (string, error) {
	p, e := r.Symbolic(Tracking(remote, "HEAD"))
	if e != nil {
		return "", e
	}
	prefix := Tracking(remote, "")
	if p == nil || !strings.HasPrefix(*p, prefix) {
		return "", fmt.Errorf("REMOTE_HEAD_MISSING: initialize the remote default branch")
	}
	b := strings.TrimPrefix(*p, prefix)
	tip, e := r.Object(*p)
	if e != nil {
		return "", e
	}
	if tip == nil || b == "HEAD" {
		return "", fmt.Errorf("REMOTE_HEAD_AMBIGUOUS")
	}
	return b, nil
}

var objectPattern = regexp.MustCompile("^(?:[0-9a-f]{40}|[0-9a-f]{64})$")

func ValidOID(s *string) bool { return s == nil || objectPattern.MatchString(*s) }
func ValidRef(s string) bool {
	if !strings.HasPrefix(s, "refs/") || strings.ContainsAny(s, " \x00\t\n\r~^:?*[\\") || strings.Contains(s, "..") || strings.Contains(s, "@{") || strings.HasSuffix(s, ".") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
		for _, c := range part {
			if c < 32 || c == 127 {
				return false
			}
		}
	}
	return true
}
func ValidateUpdates(updates []Update) error {
	seen := map[string]bool{}
	for _, u := range updates {
		if seen[u.Ref] || !ValidRef(u.Ref) || !ValidOID(u.ExpectedOld) || !ValidOID(u.Proposed) || (u.ExpectedOld == nil && u.Proposed == nil) {
			return fmt.Errorf("INVALID_REF_UPDATE: %s", u.Ref)
		}
		seen[u.Ref] = true
	}
	return nil
}
func (r *Repository) UpdateRefs(updates []Update) error {
	if len(updates) == 0 {
		return nil
	}
	if e := ValidateUpdates(updates); e != nil {
		return e
	}
	var lines = []string{"start"}
	for _, u := range updates {
		switch {
		case u.ExpectedOld == nil:
			lines = append(lines, "create "+u.Ref+" "+*u.Proposed)
		case u.Proposed == nil:
			lines = append(lines, "delete "+u.Ref+" "+*u.ExpectedOld)
		default:
			lines = append(lines, "update "+u.Ref+" "+*u.Proposed+" "+*u.ExpectedOld)
		}
	}
	lines = append(lines, "prepare", "commit")
	_, e := r.Mutate([]byte(strings.Join(lines, "\n")+"\n"), "update-ref", "--stdin")
	return e
}
func (r *Repository) Push(remote string, updates []RemoteUpdate, dry bool) error {
	if len(updates) == 0 {
		return nil
	}
	check := make([]Update, len(updates))
	for i, u := range updates {
		check[i] = Update{u.Ref, u.Expected, u.Proposed}
	}
	if e := ValidateUpdates(check); e != nil {
		return e
	}
	args := []string{"push", "--atomic"}
	if dry {
		args = append(args, "--dry-run")
	}
	for _, u := range updates {
		args = append(args, "--force-with-lease="+u.Ref+":"+Value(u.Expected))
	}
	args = append(args, remote)
	for _, u := range updates {
		args = append(args, Value(u.Proposed)+":"+u.Ref)
	}
	_, e := r.Mutate(nil, args...)
	return e
}
func (r *Repository) BranchConfigKeys(branch string) ([]string, error) {
	b, e := r.Raw(nil, "config", "--local", "--name-only", "--null", "--get-regexp", "^branch\\."+regexp.QuoteMeta(branch)+"\\.[^.]+$")
	var ge *Error
	if errors.As(e, &ge) && ge.Code == 1 {
		return []string{}, nil
	}
	if e != nil {
		return nil, e
	}
	v := NulList(b)
	slices.Sort(v)
	return slices.Compact(v), nil
}
