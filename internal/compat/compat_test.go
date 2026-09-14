package compat

import (
	"strings"
	"testing"
)

// doc builds a minimal AsyncAPI document with one contract whose payload is
// the given schema fragment.
func doc(t *testing.T, contract, schema string) Doc {
	t.Helper()
	body := `
asyncapi: 3.0.0
channels:
  ` + contract + `:
    address: ` + contract + `
    messages:
      m:
        $ref: '#/components/messages/m'
components:
  messages:
    m:
      payload:
        $ref: '#/components/schemas/P'
  schemas:
    P:
` + schema
	d, err := Parse([]byte(body), "test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return d
}

const base = `      type: object
      required:
      - orderId
      properties:
        orderId:
          type: string
        note:
          type: string
`

func find(changes []Change, path string) *Change {
	for i := range changes {
		if changes[i].Path == path {
			return &changes[i]
		}
	}
	return nil
}

func TestBreakingChanges(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		path   string
		want   string
	}{
		{"field removed", `      type: object
      required:
      - orderId
      properties:
        orderId:
          type: string
`, "note", "field removed"},
		{"field became required", `      type: object
      required:
      - orderId
      - note
      properties:
        orderId:
          type: string
        note:
          type: string
`, "note", "became required"},
		{"type changed", `      type: object
      required:
      - orderId
      properties:
        orderId:
          type: integer
        note:
          type: string
`, "orderId", "type changed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			changes := Compare(doc(t, "orders.Placed", base), doc(t, "orders.Placed", c.schema))
			if !Breaking(changes) {
				t.Fatalf("expected a breaking change, got %v", changes)
			}
			got := find(changes, c.path)
			if got == nil || !got.Breaking || !strings.Contains(got.Detail, c.want) {
				t.Errorf("expected %q at %q, got %v", c.want, c.path, changes)
			}
		})
	}
}

func TestSafeChanges(t *testing.T) {
	widened := `      type: object
      properties:
        orderId:
          type: string
        note:
          type: string
        tenant:
          type: string
`
	changes := Compare(doc(t, "orders.Placed", base), doc(t, "orders.Placed", widened))
	if Breaking(changes) {
		t.Errorf("adding an optional field and dropping a requirement must be safe: %v", changes)
	}
	if find(changes, "tenant") == nil {
		t.Errorf("the added field should still be reported: %v", changes)
	}
}

func TestContractRemovedAndAdded(t *testing.T) {
	changes := Compare(doc(t, "orders.Placed", base), doc(t, "orders.Accepted", base))
	if !Breaking(changes) {
		t.Fatalf("removing a contract must break: %v", changes)
	}
	var removed, added bool
	for _, c := range changes {
		if c.Contract == "orders.Placed" && c.Breaking {
			removed = true
		}
		if c.Contract == "orders.Accepted" && !c.Breaking {
			added = true
		}
	}
	if !removed || !added {
		t.Errorf("expected one removal and one addition, got %v", changes)
	}
}

// Identical documents are the common CI case and must be silent.
func TestNoChanges(t *testing.T) {
	if changes := Compare(doc(t, "orders.Placed", base), doc(t, "orders.Placed", base)); len(changes) > 0 {
		t.Errorf("identical documents reported %v", changes)
	}
}

// groupDoc builds a document whose single contract carries one `receive`
// operation under the given group.
func groupDoc(t *testing.T, group string) Doc {
	t.Helper()
	body := `
asyncapi: 3.0.0
channels:
  orders.Placed:
    address: orders.Placed
    messages:
      m:
        $ref: '#/components/messages/m'
operations:
  Audit_Record_Receive:
    action: receive
    channel:
      $ref: '#/channels/orders.Placed'
    x-craftgo-group: ` + group + `
components:
  messages:
    m:
      payload:
        $ref: '#/components/schemas/P'
  schemas:
    P:
` + base
	d, err := Parse([]byte(body), "test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return d
}

func TestReceiveOperationIsLoaded(t *testing.T) {
	got := groupDoc(t, "orders-Audit-Record").Receives["Audit_Record_Receive"]
	if got.Contract != "orders.Placed" || got.Group != "orders-Audit-Record" {
		t.Errorf("receive = %+v", got)
	}
}

// The group is where the broker keeps the consumer's position, so a
// renamed one leaves it with none.
func TestConsumerGroupRenameBreaks(t *testing.T) {
	changes := Compare(groupDoc(t, "orders-Audit-Record"), groupDoc(t, "audit-worker"))
	if !Breaking(changes) {
		t.Fatalf("a renamed consumer group must break: %v", changes)
	}
	if !strings.Contains(changes[0].Detail, "no committed position") {
		t.Errorf("detail = %q", changes[0].Detail)
	}
}

func TestConsumerGroupUnchangedIsSilent(t *testing.T) {
	if changes := Compare(groupDoc(t, "orders-Audit-Record"), groupDoc(t, "orders-Audit-Record")); len(changes) > 0 {
		t.Errorf("an unchanged group reported %v", changes)
	}
}
