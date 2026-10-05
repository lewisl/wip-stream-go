package workflow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lewisl/wip-stream-go/internal/operations"
)

func TestMalformedReceiptBlocksMutation(t *testing.T) {
	f := newFixture(t)
	common, e := f.first.CommonDir()
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(common, "wipstream", "operations", "corrupt.json")
	// Intentionally malformed fixture, never a receipt in a real project.
	if e = os.WriteFile(p, []byte("{}"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e = Save(f.first, options(""))
	requireError(t, e, "INVALID_OPERATION_RECEIPT")
}

func TestInteroperableLockRecordBlocksOtherCommands(t *testing.T) {
	f := newFixture(t)
	release, e := acquire(f.first, "TypeScript-shaped lock")
	if e != nil {
		t.Fatal(e)
	}
	_, e = Get(f.first)
	requireError(t, e, "COMMAND_IN_PROGRESS")
	if e = release(); e != nil {
		t.Fatal(e)
	}
}

// Shared guards apply to every ordinary command, as in repository-safety.test.js.
func TestCommandsRefuseIncompleteOperation(t *testing.T) {
	for _, name := range []string{"init", "get", "save", "start", "finish", "update", "reconcile", "condense"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			plan := operations.NewPlan("Get from Remote")
			plan.Checkout = operations.Checkout{Before: "main", After: "main"}
			receipt, err := operations.Begin(f.first, plan)
			if err != nil {
				t.Fatal(err)
			}
			if err = operations.Phase(f.first, receipt, "before-remote-fetch"); err != nil {
				t.Fatal(err)
			}
			unchangedRefusal(t, f, "INCOMPLETE_WIPSTREAM_OPERATION", func() (Result, error) {
				switch name {
				case "init":
					return Init(f.first, Options{})
				case "get":
					return Get(f.first)
				case "save":
					return Save(f.first, options("blocked"))
				case "start":
					return Start(f.first, "topic")
				case "finish":
					return Finish(f.first, options("blocked"))
				case "update":
					return Update(f.first, Options{})
				case "reconcile":
					return Reconcile(f.first, Options{})
				default:
					return Condense(f.first, options("blocked"))
				}
			})
		})
	}
}
