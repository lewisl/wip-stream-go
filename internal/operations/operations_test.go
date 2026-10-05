package operations

import (
	"encoding/json"
	"github.com/lewisl/wip-stream-go/internal/git"
	"strings"
	"testing"
)

func receiptDocument(t *testing.T) map[string]any {
	t.Helper()
	p := NewPlan("Commit and Save")
	p.OperationID = "fixture-operation"
	p.LocalRefUpdates = []git.Update{{Ref: "refs/heads/feature", ExpectedOld: git.Ptr(strings.Repeat("a", 40)), Proposed: git.Ptr(strings.Repeat("b", 40))}}
	p.RemoteRefUpdates = []git.RemoteUpdate{{Ref: "refs/heads/feature", Expected: git.Ptr(strings.Repeat("a", 40)), Proposed: git.Ptr(strings.Repeat("b", 40))}}
	r := Receipt{SchemaVersion: 1, Plan: p, Status: "completed", Phase: "completed", Events: []Event{{"planned", Now()}, {"completed", Now()}}}
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var doc map[string]any
	if e = json.Unmarshal(b, &doc); e != nil {
		t.Fatal(e)
	}
	return doc
}
func TestReceiptReaderRefusesAmbiguousAndMalformedTransitions(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing expectation", func(d map[string]any) {
			u := d["plan"].(map[string]any)["localRefUpdates"].([]any)[0].(map[string]any)
			delete(u, "expectedOld")
		}},
		{"missing remote expectation", func(d map[string]any) {
			u := d["plan"].(map[string]any)["remoteRefUpdates"].([]any)[0].(map[string]any)
			delete(u, "expected")
		}},
		{"abbreviated object", func(d map[string]any) {
			d["plan"].(map[string]any)["localRefUpdates"].([]any)[0].(map[string]any)["proposed"] = "abc123"
		}},
		{"path traversal ref", func(d map[string]any) {
			d["plan"].(map[string]any)["localRefUpdates"].([]any)[0].(map[string]any)["ref"] = "refs/heads/../escape"
		}},
		{"duplicate refs", func(d map[string]any) {
			p := d["plan"].(map[string]any)
			u := p["localRefUpdates"].([]any)
			p["localRefUpdates"] = append(u, u[0])
		}},
		{"null creation and deletion", func(d map[string]any) {
			u := d["plan"].(map[string]any)["localRefUpdates"].([]any)[0].(map[string]any)
			u["expectedOld"] = nil
			u["proposed"] = nil
		}},
		{"future schema", func(d map[string]any) { d["plan"].(map[string]any)["schemaVersion"] = 3 }},
		{"filename mismatch", func(d map[string]any) { d["plan"].(map[string]any)["operationId"] = "other-operation" }},
		{"missing configuration values", func(d map[string]any) {
			d["plan"].(map[string]any)["configurationChanges"] = []any{map[string]any{"key": "wipstream.remote", "after": []string{"origin"}}}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := receiptDocument(t)
			c.mutate(doc)
			b, e := json.Marshal(doc)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = Decode(b, "fixture-operation"); e == nil {
				t.Fatal("accepted unsafe recovery metadata")
			}
		})
	}
}
func TestLegacyPlanNormalizesExactLeasesWithoutChangingInput(t *testing.T) {
	doc := receiptDocument(t)
	p := doc["plan"].(map[string]any)
	p["schemaVersion"] = 1
	u := p["remoteRefUpdates"].([]any)[0].(map[string]any)
	p["remoteLeases"] = []any{map[string]any{"ref": u["ref"], "expected": u["expected"]}}
	delete(u, "expected")
	b, e := json.Marshal(doc)
	if e != nil {
		t.Fatal(e)
	}
	r, e := Decode(b, "fixture-operation")
	if e != nil {
		t.Fatal(e)
	}
	if r.Plan.SchemaVersion != 2 || git.Value(r.Plan.RemoteRefUpdates[0].Expected) != strings.Repeat("a", 40) {
		t.Fatal("legacy lease lost")
	}
	p["remoteLeases"] = []any{}
	b, e = json.Marshal(doc)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Decode(b, "fixture-operation"); e == nil {
		t.Fatal("accepted unmatched legacy publication")
	}
}
