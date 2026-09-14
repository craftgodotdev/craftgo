package nats

import (
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

func TestAGroupWithARejectedCharacterIsRefused(t *testing.T) {
	for _, group := range []string{"my.group", "my group", "my>group", "my*group", "my/group"} {
		err := checkGroup(group)
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

func TestAnEmptyGroupIsRefused(t *testing.T) {
	if err := checkGroup(""); err == nil {
		t.Fatal("an empty group has no durable name")
	}
}

func TestAnOverlongGroupIsRefusedLocally(t *testing.T) {
	err := checkGroup(strings.Repeat("g", 256))
	if err == nil {
		t.Fatal("a group over 255 characters must be refused")
	}
	if !strings.Contains(err.Error(), "255") {
		t.Errorf("refusal does not mention the limit: %v", err)
	}
}

func TestAPlainGroupIsADurableName(t *testing.T) {
	for _, group := range []string{"store-front-store", "notifications-NotificationService-SendReceipt", "g_1"} {
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
	if got := filterOf(multi); !equalSets(got, []string{"orders.Placed", "orders.Shipped"}) {
		t.Errorf("filterOf(multi) = %v, want sorted", got)
	}
	if got := filterOf(jetstream.ConsumerConfig{}); got != nil {
		t.Errorf("filterOf(none) = %v, want nil", got)
	}
}
