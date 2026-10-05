// Package operations implements WipStream's shared durable operation protocol.
package operations

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lewisl/wip-stream-go/internal/git"
)

type Checkout struct {
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}
type Checkpoint struct {
	Branch  string `json:"branch"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Message string `json:"message"`
}
type Restoration struct {
	Before string `json:"before"`
	After  string `json:"after"`
}
type Configuration struct {
	Key    string   `json:"key"`
	Before []string `json:"before"`
	After  []string `json:"after"`
}
type RemoteHead struct {
	Remote string  `json:"remote"`
	Before *string `json:"before"`
	After  *string `json:"after"`
}
type Effect struct {
	Kind        string `json:"kind"`
	Ref         string `json:"ref,omitempty"`
	Description string `json:"description"`
}
type Plan struct {
	SchemaVersion         int                `json:"schemaVersion"`
	OperationID           string             `json:"operationId"`
	Command               string             `json:"command"`
	CreatedAt             string             `json:"createdAt"`
	LocalRefUpdates       []git.Update       `json:"localRefUpdates"`
	RemoteRefUpdates      []git.RemoteUpdate `json:"remoteRefUpdates"`
	Checkpoint            *Checkpoint        `json:"checkpoint,omitempty"`
	CheckpointRestoration *Restoration       `json:"checkpointRestoration,omitempty"`
	RemoteHead            *RemoteHead        `json:"remoteHead,omitempty"`
	RemoteAdoption        json.RawMessage    `json:"remoteAdoption,omitempty"`
	ConfigurationChanges  []Configuration    `json:"configurationChanges"`
	Checkout              Checkout           `json:"checkout"`
	DestructiveEffects    []Effect           `json:"destructiveEffects"`
}
type Event struct {
	Phase      string `json:"phase"`
	RecordedAt string `json:"recordedAt"`
}
type PendingMerge struct {
	Kind              string   `json:"kind"`
	Command           string   `json:"command"`
	Branch            string   `json:"branch"`
	MergeTarget       string   `json:"mergeTarget"`
	MergeTargetCommit string   `json:"mergeTargetCommit,omitempty"`
	PreHead           string   `json:"preHead"`
	PreIndexTree      string   `json:"preIndexTree"`
	PreStatus         string   `json:"preStatus"`
	Conflicts         []string `json:"conflicts"`
}
type Outcome struct {
	AdditionalLocalRefUpdates []git.Update `json:"additionalLocalRefUpdates"`
	CompletedLocalRefs        []git.Ref    `json:"completedLocalRefs,omitempty"`
	CompletedRemoteRefs       []git.Ref    `json:"completedRemoteRefs,omitempty"`
	CompletedCheckout         string       `json:"completedCheckout,omitempty"`
	CompletedStatus           *string      `json:"completedStatus,omitempty"`
}
type Recovery struct {
	Resolution  string `json:"resolution"`
	Branch      string `json:"branch,omitempty"`
	Head        string `json:"head,omitempty"`
	MergeCommit string `json:"mergeCommit,omitempty"`
}
type Receipt struct {
	SchemaVersion int           `json:"schemaVersion"`
	Plan          Plan          `json:"plan"`
	Phase         string        `json:"phase"`
	Status        string        `json:"status"`
	Events        []Event       `json:"events"`
	PendingMerge  *PendingMerge `json:"pendingMerge,omitempty"`
	Outcome       *Outcome      `json:"outcome,omitempty"`
	CompletedAt   string        `json:"completedAt,omitempty"`
	Recovery      *Recovery     `json:"recovery,omitempty"`
}

func Now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }
func ID() string  { return fmt.Sprintf("%x", randomBytes()) }
func randomBytes() []byte {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return b
}

var idPattern = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$")

func NewPlan(command string) Plan {
	return Plan{SchemaVersion: 2, OperationID: ID(), Command: command, CreatedAt: Now(), LocalRefUpdates: []git.Update{}, RemoteRefUpdates: []git.RemoteUpdate{}, ConfigurationChanges: []Configuration{}, DestructiveEffects: []Effect{}}
}
func (r *Receipt) Incomplete() bool { return r.Status == "planned" || r.Status == "in-progress" }
func required(raw map[string]json.RawMessage, keys ...string) error {
	for _, key := range keys {
		v, ok := raw[key]
		if !ok || string(v) == "null" {
			return fmt.Errorf("missing protocol field %s", key)
		}
	}
	return nil
}
func Decode(data []byte, id string) (*Receipt, error) {
	var raw map[string]json.RawMessage
	if e := json.Unmarshal(data, &raw); e != nil {
		return nil, e
	}
	if e := required(raw, "schemaVersion", "plan", "phase", "status", "events"); e != nil {
		return nil, e
	}
	var planRaw map[string]json.RawMessage
	if e := json.Unmarshal(raw["plan"], &planRaw); e != nil {
		return nil, e
	}
	if e := required(planRaw, "schemaVersion", "operationId", "command", "createdAt", "localRefUpdates", "remoteRefUpdates", "configurationChanges", "checkout", "destructiveEffects"); e != nil {
		return nil, e
	}
	var r Receipt
	if e := json.Unmarshal(data, &r); e != nil {
		return nil, e
	}
	if r.SchemaVersion != 1 || (r.Plan.SchemaVersion != 1 && r.Plan.SchemaVersion != 2) || !idPattern.MatchString(r.Plan.OperationID) || r.Plan.OperationID != id || strings.TrimSpace(r.Plan.Command) == "" || r.Events == nil || r.Plan.LocalRefUpdates == nil || r.Plan.RemoteRefUpdates == nil || r.Plan.ConfigurationChanges == nil || r.Plan.DestructiveEffects == nil {
		return nil, fmt.Errorf("invalid operation receipt")
	}
	if !slices.Contains([]string{"planned", "in-progress", "completed", "aborted", "undone", "recovered"}, r.Status) {
		return nil, fmt.Errorf("invalid receipt status")
	}
	// Required nullable transition fields must be present; missing values are not null.
	var locals []map[string]json.RawMessage
	if e := json.Unmarshal(planRaw["localRefUpdates"], &locals); e != nil {
		return nil, e
	}
	for _, u := range locals {
		for _, key := range []string{"ref", "expectedOld", "proposed"} {
			if _, ok := u[key]; !ok {
				return nil, fmt.Errorf("missing local update field %s", key)
			}
		}
	}
	if e := git.ValidateUpdates(r.Plan.LocalRefUpdates); e != nil {
		return nil, e
	}
	if r.Plan.SchemaVersion == 1 {
		var leases []struct {
			Ref      string  `json:"ref"`
			Expected *string `json:"expected"`
		}
		if e := required(planRaw, "remoteLeases"); e != nil {
			return nil, e
		}
		if e := json.Unmarshal(planRaw["remoteLeases"], &leases); e != nil {
			return nil, e
		}
		var rawLeases []map[string]json.RawMessage
		if e := json.Unmarshal(planRaw["remoteLeases"], &rawLeases); e != nil {
			return nil, e
		}
		for _, lease := range rawLeases {
			if _, ok := lease["expected"]; !ok {
				return nil, fmt.Errorf("missing legacy lease expectation")
			}
		}
		byRef := map[string]*string{}
		for _, l := range leases {
			if _, ok := byRef[l.Ref]; ok {
				return nil, fmt.Errorf("duplicate lease")
			}
			byRef[l.Ref] = l.Expected
		}
		for i, u := range r.Plan.RemoteRefUpdates {
			v, ok := byRef[u.Ref]
			if !ok {
				return nil, fmt.Errorf("unmatched legacy lease")
			}
			r.Plan.RemoteRefUpdates[i].Expected = v
			delete(byRef, u.Ref)
		}
		if len(byRef) > 0 {
			return nil, fmt.Errorf("unmatched legacy leases")
		}
		r.Plan.SchemaVersion = 2
	} else {
		var updates []map[string]json.RawMessage
		if e := json.Unmarshal(planRaw["remoteRefUpdates"], &updates); e != nil {
			return nil, e
		}
		for _, update := range updates {
			for _, key := range []string{"ref", "expected", "proposed"} {
				if _, ok := update[key]; !ok {
					return nil, fmt.Errorf("missing remote update field %s", key)
				}
			}
		}
	}
	check := []git.Update{}
	for _, u := range r.Plan.RemoteRefUpdates {
		check = append(check, git.Update{Ref: u.Ref, ExpectedOld: u.Expected, Proposed: u.Proposed})
	}
	if e := git.ValidateUpdates(check); e != nil {
		return nil, e
	}
	for _, c := range r.Plan.ConfigurationChanges {
		if c.Key == "" || c.Before == nil || c.After == nil {
			return nil, fmt.Errorf("invalid configuration transition")
		}
	}
	if p := r.PendingMerge; p != nil {
		if p.Kind != "merge" || (p.Command != "Update from Parent" && p.Command != "Reconcile with Remote") ||
			p.Branch == "" || !git.ValidRef(p.MergeTarget) || !git.ValidOID(git.Ptr(p.PreHead)) ||
			!git.ValidOID(git.Ptr(p.PreIndexTree)) || (p.MergeTargetCommit != "" && !git.ValidOID(git.Ptr(p.MergeTargetCommit))) ||
			p.Conflicts == nil {
			return nil, fmt.Errorf("invalid pending merge evidence")
		}
	}
	if r.Outcome != nil {
		if r.Outcome.AdditionalLocalRefUpdates == nil {
			return nil, fmt.Errorf("missing outcome updates")
		}
		if e := git.ValidateUpdates(r.Outcome.AdditionalLocalRefUpdates); e != nil {
			return nil, e
		}
	}
	return &r, nil
}
func Directory(repo *git.Repository) (string, error) {
	d, e := repo.CommonDir()
	return filepath.Join(d, "wipstream", "operations"), e
}
func Path(repo *git.Repository, id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", fmt.Errorf("INVALID_OPERATION_ID")
	}
	d, e := Directory(repo)
	return filepath.Join(d, id+".json"), e
}
func Read(repo *git.Repository, id string) (*Receipt, error) {
	p, e := Path(repo, id)
	if e != nil {
		return nil, e
	}
	data, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	r, e := Decode(data, id)
	if e != nil {
		return nil, fmt.Errorf("INVALID_OPERATION_RECEIPT: %s: %w", p, e)
	}
	return r, nil
}
func Write(repo *git.Repository, r *Receipt, exclusive bool) error {
	p, e := Path(repo, r.Plan.OperationID)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	// Validate our own output before publishing it as recovery data.
	if _, e = Decode(b, r.Plan.OperationID); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".receipt-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if exclusive {
		e = os.Link(f.Name(), p)
	} else {
		e = os.Rename(f.Name(), p)
	}
	if e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(p))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func Begin(repo *git.Repository, p Plan) (*Receipt, error) {
	r := &Receipt{SchemaVersion: 1, Plan: p, Phase: "planned", Status: "planned", Events: []Event{{"planned", Now()}}}
	return r, Write(repo, r, true)
}
func List(repo *git.Repository) ([]*Receipt, error) {
	d, e := Directory(repo)
	if e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(d)
	if os.IsNotExist(e) {
		return []*Receipt{}, nil
	}
	if e != nil {
		return nil, e
	}
	receipts := []*Receipt{}
	for _, ent := range entries {
		if strings.HasSuffix(ent.Name(), ".json") {
			r, e := Read(repo, strings.TrimSuffix(ent.Name(), ".json"))
			if e != nil {
				return nil, e
			}
			receipts = append(receipts, r)
		}
	}
	return receipts, nil
}
func Phase(repo *git.Repository, r *Receipt, next string) error {
	valid := false
	if next == "completed" || strings.HasPrefix(next, "before-") {
		valid = r.Phase == "planned" || strings.HasPrefix(r.Phase, "after-")
	} else if strings.HasPrefix(next, "after-") {
		valid = r.Phase == "before-"+strings.TrimPrefix(next, "after-")
	}
	if !r.Incomplete() || !valid {
		return fmt.Errorf("INVALID_OPERATION_PHASE: %s -> %s", r.Phase, next)
	}
	r.Phase = next
	r.Status = "in-progress"
	if next == "completed" {
		r.Status = next
		r.CompletedAt = Now()
	}
	r.Events = append(r.Events, Event{next, Now()})
	return Write(repo, r, false)
}
func Boundary(repo *git.Repository, r *Receipt, name string, fn func() error) error {
	if e := Phase(repo, r, "before-"+name); e != nil {
		return e
	}
	if e := fn(); e != nil {
		return e
	}
	return Phase(repo, r, "after-"+name)
}
func Close(repo *git.Repository, r *Receipt, status string, recovery *Recovery) error {
	if status == "undone" {
		if r.Status != "completed" {
			return fmt.Errorf("OPERATION_NOT_COMPLETED")
		}
	} else if !r.Incomplete() {
		return fmt.Errorf("OPERATION_NOT_IN_PROGRESS")
	}
	r.Phase = status
	r.Status = status
	r.Recovery = recovery
	if status != "undone" {
		r.CompletedAt = Now()
	}
	r.Events = append(r.Events, Event{status, Now()})
	return Write(repo, r, false)
}
func Recorded(repo *git.Repository, p Plan, fn func(*Receipt) error) error {
	r, e := Begin(repo, p)
	if e != nil {
		return e
	}
	e = fn(r)
	if r.Status == "planned" && r.Phase == "planned" {
		if closeErr := Close(repo, r, "aborted", nil); e == nil {
			e = closeErr
		}
	}
	return e
}
func RecoveryRef(id string, ordinal int) string {
	return fmt.Sprintf("refs/wipstream/recovery/%s/%04d", id, ordinal)
}
func ApplyLocal(repo *git.Repository, r *Receipt) error {
	updates := []git.Update{}
	for i, u := range r.Plan.LocalRefUpdates {
		if u.ExpectedOld != nil && !git.Same(u.ExpectedOld, u.Proposed) {
			updates = append(updates, git.Update{Ref: RecoveryRef(r.Plan.OperationID, i), Proposed: u.ExpectedOld})
		}
	}
	updates = append(updates, r.Plan.LocalRefUpdates...)
	return Boundary(repo, r, "local-refs", func() error { return repo.UpdateRefs(updates) })
}
func ApplyConfig(repo *git.Repository, r *Receipt) error {
	if len(r.Plan.ConfigurationChanges) == 0 {
		return nil
	}
	return Boundary(repo, r, "configuration", func() error {
		for _, c := range r.Plan.ConfigurationChanges {
			before, e := repo.Config(c.Key)
			if e != nil {
				return e
			}
			if !slices.Equal(before, c.Before) {
				return fmt.Errorf("CONFIGURATION_CHANGED: %s", c.Key)
			}
			if e = repo.SetConfig(c.Key, c.After); e != nil {
				return e
			}
			after, e := repo.Config(c.Key)
			if e != nil {
				return e
			}
			if !slices.Equal(after, c.After) {
				return fmt.Errorf("CONFIGURATION_VERIFICATION_FAILED: %s", c.Key)
			}
		}
		return nil
	})
}
func ApplyHead(repo *git.Repository, r *Receipt) error {
	h := r.Plan.RemoteHead
	if h == nil {
		return nil
	}
	return Boundary(repo, r, "remote-head", func() error {
		ref := git.Tracking(h.Remote, "HEAD")
		old, e := repo.Symbolic(ref)
		if e != nil {
			return e
		}
		if !git.Same(old, h.Before) {
			return fmt.Errorf("REMOTE_HEAD_CHANGED")
		}
		if old == nil {
			obj, e := repo.Object(ref)
			if e != nil {
				return e
			}
			if obj != nil {
				return fmt.Errorf("REMOTE_HEAD_CHANGED")
			}
		}
		if h.After == nil {
			if old != nil {
				_, e = repo.Mutate(nil, "symbolic-ref", "--delete", ref)
			}
		} else {
			if !git.ValidRef(*h.After) || !strings.HasPrefix(*h.After, git.Tracking(h.Remote, "")) || *h.After == ref {
				return fmt.Errorf("INVALID_REMOTE_HEAD")
			}
			_, e = repo.Mutate(nil, "symbolic-ref", ref, *h.After)
		}
		return e
	})
}
func Complete(repo *git.Repository, r *Receipt) error {
	if r.Outcome == nil {
		r.Outcome = &Outcome{AdditionalLocalRefUpdates: []git.Update{}}
	}
	var e error
	r.Outcome.CompletedLocalRefs, e = repo.Refs("refs/heads/")
	if e != nil {
		return e
	}
	r.Outcome.CompletedRemoteRefs, e = repo.Refs("refs/remotes/")
	if e != nil {
		return e
	}
	r.Outcome.CompletedCheckout, e = repo.Branch()
	if e != nil {
		return e
	}
	status, e := repo.Status()
	if e != nil {
		return e
	}
	r.Outcome.CompletedStatus = &status
	if e = Write(repo, r, false); e != nil {
		return e
	}
	if e = Phase(repo, r, "completed"); e != nil {
		return e
	}
	receipts, e := List(repo)
	if e != nil {
		return e
	}
	completed := []*Receipt{}
	for _, v := range receipts {
		if v.Status == "completed" {
			completed = append(completed, v)
		}
	}
	slices.SortFunc(completed, func(a, b *Receipt) int { return strings.Compare(b.CompletedAt, a.CompletedAt) })
	for i := 50; i < len(completed); i++ {
		p, e := Path(repo, completed[i].Plan.OperationID)
		if e != nil {
			return e
		}
		if e = os.Remove(p); e != nil {
			return e
		}
	}
	return nil
}
