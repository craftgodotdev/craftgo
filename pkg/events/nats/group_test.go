package nats

import (
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	events "github.com/craftgodotdev/craftgo/pkg/events"
)

func TestAGroupWithARejectedCharacterIsRefused(t *testing.T) {
	for _, group := range []events.Group{"my.group", "my group", "my>group", "my*group", "my/group"} {
		err := checkGroup(group)
		if err == nil {
			t.Errorf("group %q must be refused", group)
			continue
		}
		if !strings.Contains(err.Error(), string(group)) {
			t.Errorf("refusal does not quote what the user wrote: %v", err)
		}
		if !strings.Contains(err.Error(), "yours to rename") {
			t.Errorf("refusal does not say whose it is to fix: %v", err)
		}
	}
}

func TestAnEmptyGroupIsRefused(t *testing.T) {
	if err := checkGroup(""); err == nil {
		t.Fatal("an empty group has no durable name")
	}
}

func TestAnOverlongGroupIsRefusedLocally(t *testing.T) {
	err := checkGroup(events.Group(strings.Repeat("g", 256)))
	if err == nil {
		t.Fatal("a group over 255 characters must be refused")
	}
	if !strings.Contains(err.Error(), "255") {
		t.Errorf("refusal does not mention the limit: %v", err)
	}
}

func TestAPlainGroupIsADurableName(t *testing.T) {
	for _, group := range []events.Group{"store-front-store", "notifications-NotificationService-SendReceipt", "g_1"} {
		if err := checkGroup(group); err != nil {
			t.Errorf("group %q refused: %v", group, err)
		}
	}
}

func TestOneSubjectUsesTheSingleFilterField(t *testing.T) {
	var cfg jetstream.ConsumerConfig
	setFilter(&cfg, []string{"orders.Placed"})
	if cfg.FilterSubject != "orders.Placed" || cfg.FilterSubjects != nil {
		t.Errorf("cfg = %+v, want FilterSubject alone", cfg)
	}
	setFilter(&cfg, []string{"orders.Placed", "orders.Shipped"})
	if cfg.FilterSubject != "" || len(cfg.FilterSubjects) != 2 {
		t.Errorf("cfg = %+v, want FilterSubjects alone", cfg)
	}
}

func TestTheFilterSetIsReadFromEitherField(t *testing.T) {
	single := jetstream.ConsumerConfig{FilterSubject: "orders.Placed"}
	if got := filterOf(single); len(got) != 1 || got[0] != "orders.Placed" {
		t.Errorf("filterOf(single) = %v", got)
	}
	multi := jetstream.ConsumerConfig{FilterSubjects: []string{"orders.Shipped", "orders.Placed"}}
	if got := filterOf(multi); !slices.Equal(got, []string{"orders.Placed", "orders.Shipped"}) {
		t.Errorf("filterOf(multi) = %v, want sorted", got)
	}
	if got := filterOf(jetstream.ConsumerConfig{}); got != nil {
		t.Errorf("filterOf(none) = %v, want nil", got)
	}
}

func TestAdoptingComparesTheFilterSets(t *testing.T) {
	both := []string{"orders.Placed", "orders.Shipped"}
	cases := []struct {
		name        string
		carried     []string
		planned     []string
		allowNarrow bool
		want        adoption
	}{
		{"equal", both, both, false, adoptAsIs},
		{"a consumer was added", []string{"orders.Placed"}, both, false, repoint},
		{"a consumer was removed", both, []string{"orders.Placed"}, false, refuse},
		{"only partly overlapping", both, []string{"orders.Placed", "orders.Paid"}, false, refuse},
		{"no filter at all is every subject", nil, both, false, refuse},
		{"a removal the group allows", both, []string{"orders.Placed"}, true, repoint},
		{"an overlap the group allows", both, []string{"orders.Paid"}, true, repoint},
		{"equal wins over allowNarrow", both, both, true, adoptAsIs},
	}
	for _, c := range cases {
		if got := adopting(c.carried, c.planned, c.allowNarrow); got != c.want {
			t.Errorf("%s: adopting(%v, %v, %v) = %v, want %v", c.name, c.carried, c.planned, c.allowNarrow, got, c.want)
		}
	}
}

func TestSubsetIsEveryElementOfTheFirst(t *testing.T) {
	both := []string{"orders.Placed", "orders.Shipped"}
	if !subset([]string{"orders.Placed"}, both) {
		t.Error("a smaller set is a subset")
	}
	if !subset(nil, both) {
		t.Error("the empty set is a subset")
	}
	if subset(both, []string{"orders.Placed"}) {
		t.Error("a larger set is not a subset")
	}
	if subset([]string{"orders.Paid"}, both) {
		t.Error("a disjoint set is not a subset")
	}
}
