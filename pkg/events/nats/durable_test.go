package nats

import (
	"strings"
	"testing"
)

// THE CONTRACT MUST BE IN THE NAME. A durable carries exactly ONE filter
// subject, so two subscriptions sharing a name with different contracts
// do not divide work - the second retargets the first, and the first then
// receives the other contract's messages and decodes them as its own
// type. craftgo actively encourages one group across several contracts,
// so a name built from the group alone breaks the documented idiom on the
// first design that uses it.
func TestOneGroupOnTwoContractsGetsTwoDurables(t *testing.T) {
	placed, err := durableName("stock-movements", "orders.Placed")
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := durableName("stock-movements", "orders.Cancelled")
	if err != nil {
		t.Fatal(err)
	}
	if placed == cancelled {
		t.Fatalf("both contracts got durable %q - the second would retarget the first", placed)
	}
	for _, name := range []string{placed, cancelled} {
		if !strings.HasPrefix(name, "stock-movements-") {
			t.Errorf("durable %q does not carry the group", name)
		}
	}
}

// Replicas share a group AND a contract, so they share one durable and
// divide the work - which is what Subscription.Group promises.
func TestReplicasOfOneSubscriptionShareADurable(t *testing.T) {
	first, _ := durableName("receipts", "orders.Placed")
	second, _ := durableName("receipts", "orders.Placed")
	if first != second {
		t.Errorf("replicas got %q and %q - they must share one durable", first, second)
	}
}

// Two groups on one contract get two durables, so each receives
// everything rather than splitting it.
func TestTwoGroupsOnOneContractGetTwoDurables(t *testing.T) {
	a, _ := durableName("receipts", "orders.Placed")
	b, _ := durableName("auditing", "orders.Placed")
	if a == b {
		t.Errorf("both groups got durable %q - they would split the stream", a)
	}
}

// A contract is SANITISED: `orders.Placed` is the ordinary shape of one,
// and its name is the design's rather than the user's to retype here.
func TestAContractIsSanitisedNotRefused(t *testing.T) {
	name, err := durableName("g", "orders.Placed")
	if err != nil {
		t.Fatalf("a dotted contract must be sanitised, not refused: %v", err)
	}
	if strings.ContainsAny(name, ".>*/\\ \t\r\n") {
		t.Errorf("durable %q still carries a character the server rejects", name)
	}
	if name != "g-orders_Placed" {
		t.Errorf("durable = %q, want g-orders_Placed", name)
	}
}

// A GROUP is refused rather than sanitised, quoting what the user wrote:
// the group is theirs to rename, and silently renaming it would move
// which consumer they resume from.
func TestAGroupWithARejectedCharacterIsRefused(t *testing.T) {
	for _, group := range []string{"my.group", "my group", "my>group", "my*group", "my/group"} {
		_, err := durableName(group, "orders.Placed")
		if err == nil {
			t.Errorf("group %q must be refused", group)
			continue
		}
		if !strings.Contains(err.Error(), group) {
			t.Errorf("refusal does not quote what the user wrote: %v", err)
		}
		if !strings.Contains(err.Error(), "yours to rename") {
			t.Errorf("refusal does not say whose it is to fix: %v", err)
		}
	}
}

// Over the server's limit is refused LOCALLY, before any round trip: the
// server's own error quotes a composed name the user has never seen.
func TestAnOverlongDurableIsRefusedLocally(t *testing.T) {
	_, err := durableName(strings.Repeat("g", 200), strings.Repeat("c", 100))
	if err == nil {
		t.Fatal("a durable over 255 characters must be refused")
	}
	for _, want := range []string{"255", "shorten"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}
