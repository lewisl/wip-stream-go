package workflow

import (
	"fmt"
	"strings"

	"github.com/lewisl/wip-stream-go/internal/operations"
)

// Describe verified effects, including a no-op, rather than merely naming the
// command. Call this only after synchronization and receipt completion succeed.
func synchronizationMessage(command, remote string, p operations.Plan) string {
	parts := []string{}
	if command == "Initialize Repository" {
		parts = append(parts, fmt.Sprintf("Initialized repository for remote %q.", remote))
	}
	if p.Checkpoint != nil {
		parts = append(parts, fmt.Sprintf("Created a checkpoint on %q.", p.Checkpoint.Branch))
	} else if command != "Get from Remote" {
		parts = append(parts, "No new checkpoint was needed.")
	}
	if len(p.RemoteRefUpdates) > 0 {
		names := []string{}
		for _, u := range p.RemoteRefUpdates {
			names = append(names, strings.TrimPrefix(u.Ref, "refs/heads/"))
		}
		parts = append(parts, fmt.Sprintf("Pushed %s to %q.", quotedNames(names), remote))
	} else if command != "Get from Remote" {
		parts = append(parts, "No push was needed.")
	}
	created, updated, deleted := []string{}, []string{}, []string{}
	for _, u := range p.LocalRefUpdates {
		name := strings.TrimPrefix(u.Ref, "refs/heads/")
		switch {
		case u.ExpectedOld == nil:
			created = append(created, name)
		case u.Proposed == nil:
			deleted = append(deleted, name)
		default:
			updated = append(updated, name)
		}
	}
	changes := []string{}
	for _, change := range []struct {
		verb  string
		names []string
	}{{"created", created}, {"updated", updated}, {"deleted", deleted}} {
		if len(change.names) > 0 {
			changes = append(changes, change.verb+" "+quotedNames(change.names))
		}
	}
	if len(changes) > 0 {
		parts = append(parts, fmt.Sprintf("Retrieved from %q: %s.", remote, strings.Join(changes, "; ")))
	} else if command == "Get from Remote" {
		parts = append(parts, fmt.Sprintf("Already up to date with %q.", remote))
	}
	parts = append(parts, fmt.Sprintf("All branches are synchronized; checked out %q.", p.Checkout.After))
	if command != "Get from Remote" {
		parts = append(parts, "Safe to resume on another computer.")
	}
	return strings.Join(parts, " ")
}

func quotedNames(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	return strings.Join(quoted, ", ")
}

func handoffFailure(checkpointCreated bool) string {
	if checkpointCreated {
		return "Checkpoint committed locally, but remote handoff did not complete. Do not resume work in another clone."
	}
	return "Remote handoff did not complete; existing local work is retained. Do not resume work in another clone."
}

func pendingMergeMessage(branch string, conflicts []string) string {
	message := fmt.Sprintf("Merge on %q needs attention.", branch)
	if len(conflicts) > 0 {
		message += " Conflicts: " + strings.Join(conflicts, ", ") + "."
	}
	return message + " Resolve and stage the files, then run 'wipstream continue'; or run 'wipstream abort' to restore the pre-merge state."
}
