// Package cli adapts command flags and terminal prompts to testable workflows.
package cli

import (
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/huh"
	"github.com/lewisl/wip-stream-go/internal/git"
	"github.com/lewisl/wip-stream-go/internal/operations"
	"github.com/lewisl/wip-stream-go/internal/workflow"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"os"
	"path/filepath"
	"strings"
)

func New() *cobra.Command {
	root := &cobra.Command{Use: "wipstream", Short: "Safely synchronize work across ordinary Git clones", SilenceUsage: true, SilenceErrors: true}
	var dir string
	var jsonOutput bool
	root.PersistentFlags().StringVarP(&dir, "repo", "C", ".", "Repository directory")
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Print structured result")
	for _, name := range []string{"init", "get", "save", "start", "finish", "update", "reconcile", "continue", "abort", "recover", "undo", "condense"} {
		commandName := name
		var message, parent, remote, authority, backup, disposition, operation string
		var yes, discard bool
		cmd := &cobra.Command{Use: name, Short: commandHelp(name), Args: cobra.NoArgs}
		if name == "start" {
			cmd.Use = "start [branch]"
			cmd.Args = cobra.MaximumNArgs(1)
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			repo, e := git.Open(cmd.Context(), dir)
			if e != nil {
				return e
			}
			opts := workflow.Options{Remote: remote, Authority: authority, BackupParent: backup, Discard: discard, Disposition: disposition, OperationID: operation}
			opts.Message = func(suggested string) (string, error) {
				if cmd.Flags().Changed("message") {
					if strings.TrimSpace(message) == "" {
						return "", fmt.Errorf("commit message cannot be blank")
					}
					return message, nil
				}
				return input("Commit message", suggested)
			}
			opts.Parent = func(suggested string) (string, error) {
				if parent != "" {
					return parent, nil
				}
				return input("Confirm parent branch", suggested)
			}
			opts.Confirm = func(preview string) (bool, error) {
				if yes {
					return true, nil
				}
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return false, fmt.Errorf("confirmation required; inspect the action and pass --yes")
				}
				var approved bool
				e := huh.NewConfirm().Title(preview).Value(&approved).Run()
				return approved, e
			}
			opts.InitChoice = func(preview string) (string, error) {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return "", fmt.Errorf("authority choice required; pass --authority local-work, remote, reconcile, or cancel")
				}
				var value string
				e := huh.NewSelect[string]().Title(preview).Options(huh.NewOption("Use the remote's version", "remote"), huh.NewOption("Commit this machine's work and save to remote", "local-work"), huh.NewOption("Resolve differences locally, then save to remote", "reconcile"), huh.NewOption("Cancel", "cancel")).Value(&value).Run()
				return value, e
			}
			opts.RemoteBackup = func() (string, bool, error) {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return "", false, fmt.Errorf("remote replacement requires --backup-parent or --discard-local-work")
				}
				var choice string
				e := huh.NewSelect[string]().Title("Preserve this project before replacing it?").
					Options(huh.NewOption("Create a verified complete backup", "copy"),
						huh.NewOption("Replace without a backup", "discard"),
						huh.NewOption("Cancel", "cancel")).Value(&choice).Run()
				if e != nil {
					return "", false, e
				}
				switch choice {
				case "copy":
					parent, e := input("Existing backup parent outside this project", filepath.Dir(repo.Root))
					return parent, false, e
				case "discard":
					approved, e := opts.Confirm("Discard tracked changes and non-ignored untracked files without a backup?")
					return "", approved, e
				default:
					return "", false, fmt.Errorf("CANCELLED: no files replaced")
				}
			}
			if commandName == "finish" && disposition == "" {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return fmt.Errorf("pass --disposition retain or delete")
				}
				e = huh.NewSelect[string]().Title("Completed branch").Options(huh.NewOption("Retain branch", "retain"), huh.NewOption("Delete branch", "delete")).Value(&opts.Disposition).Run()
				if e != nil {
					return e
				}
			}
			var result workflow.Result
			switch commandName {
			case "init":
				result, e = workflow.Init(repo, opts)
			case "get":
				result, e = workflow.Get(repo)
			case "save":
				result, e = workflow.Save(repo, opts)
			case "start":
				branch := ""
				if len(args) > 0 {
					branch = args[0]
				} else {
					branch, e = input("New branch name", "change")
					if e != nil {
						return e
					}
				}
				result, e = workflow.Start(repo, branch)
			case "finish":
				result, e = workflow.Finish(repo, opts)
			case "update":
				result, e = workflow.Update(repo, opts)
			case "reconcile":
				result, e = workflow.Reconcile(repo, opts)
			case "continue":
				result, e = workflow.Continue(repo, opts)
			case "abort":
				result, e = workflow.Abort(repo)
			case "recover":
				result, e = workflow.Recover(repo, opts)
			case "undo":
				result, e = workflow.Undo(repo, opts)
			case "condense":
				result, e = workflow.Condense(repo, opts)
			}
			if e != nil {
				if result.Message != "" && (commandName == "save" || commandName == "finish" || commandName == "reconcile" || commandName == "continue" || commandName == "init" && result.CheckpointCreated) {
					fmt.Fprintln(cmd.ErrOrStderr(), "WARNING:", result.Message)
				}
				if result.OperationID != "" {
					// A completed merge may be followed by a failed Save. Offer
					// recovery only for the actual incomplete attempt, never its
					// already completed predecessor or an automatically aborted plan.
					if receipt, readErr := operations.Read(repo, result.OperationID); readErr == nil && receipt.Incomplete() {
						if receipt.PendingMerge != nil {
							return fmt.Errorf("%w\nOperation %s is incomplete. Resolve and stage its conflicts, then run 'wipstream continue'; or run 'wipstream abort'.", e, result.OperationID)
						}
						return fmt.Errorf("%w\nOperation %s is incomplete. Inspect current files and refs. Once Git has no active operation or unresolved conflicts, run 'wipstream recover --operation %s' to keep current state, then retry.", e, result.OperationID, result.OperationID)
					}
				}
				return e
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			status := "SUCCESS:"
			if result.Pending {
				status = "PENDING:"
			}
			fmt.Fprintln(cmd.OutOrStdout(), status, result.Message)
			return nil
		}
		switch name {
		case "init":
			cmd.Flags().StringVar(&remote, "remote", "", "Git remote (defaults to existing selection or origin)")
			cmd.Flags().StringVar(&authority, "authority", "", "Authority: local-work, remote, reconcile, cancel")
			cmd.Flags().StringVar(&backup, "backup-parent", "", "Existing parent directory for a verified complete backup")
			cmd.Flags().BoolVar(&discard, "discard-local-work", false, "Explicitly approve remote replacement without a backup")
		case "finish":
			cmd.Flags().StringVar(&disposition, "disposition", "", "retain or delete completed branch")
		case "recover":
			cmd.Flags().StringVar(&operation, "operation", "", "Incomplete operation ID")
		}
		if strings.Contains("|init|save|finish|condense|reconcile|continue|", "|"+name+"|") {
			cmd.Flags().StringVarP(&message, "message", "m", "", "Commit message")
		}
		if name == "finish" || name == "update" || name == "condense" {
			cmd.Flags().StringVar(&parent, "parent", "", "Explicit parent for an imported branch")
		}
		if name == "finish" || name == "condense" || name == "recover" || name == "undo" {
			cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Approve the displayed action without prompting")
		}
		root.AddCommand(cmd)
	}
	return root
}
func input(title, suggested string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("%s requires input; use a command flag", title)
	}
	value := suggested
	e := huh.NewInput().Title(title).Value(&value).Validate(func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("value cannot be blank")
		}
		return nil
	}).Run()
	return value, e
}
func commandHelp(name string) string {
	switch name {
	case "init":
		return "Initialize all branches with an explicit authority decision"
	case "get":
		return "Retrieve safe remote changes across all branches"
	case "save":
		return "Checkpoint files and complete an atomic remote handoff"
	case "start":
		return "Start a work branch and record its parent"
	case "finish":
		return "Advance the parent and retain or delete the work branch"
	case "update":
		return "Merge the recorded parent into the work branch"
	case "reconcile":
		return "Merge divergent remote history and save"
	case "continue":
		return "Complete a recorded WipStream merge and save"
	case "abort":
		return "Abort a recorded merge and verify restoration"
	case "recover":
		return "Close an incomplete operation while keeping current state"
	case "undo":
		return "Reverse the latest eligible completed action"
	default:
		return "Replace work-branch checkpoints with one commit"
	}
}
