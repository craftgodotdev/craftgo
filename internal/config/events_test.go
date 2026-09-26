package config

import (
	"strings"
	"testing"
)

// TestEventsDefaultToASingleGoTarget checks that a manifest without events
// gets one Go target in ./internal/events.
func TestEventsDefaultToASingleGoTarget(t *testing.T) {
	cfg, err := loadManifest(t, "openapi:\n  title: X\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Events.Targets) != 1 {
		t.Fatalf("targets = %v", cfg.Events.Targets)
	}
	target := cfg.Events.Targets[0]
	if target.Lang != LangGo || target.Out != "./internal/events" {
		t.Errorf("default target = %+v", target)
	}
}

// TestEventTargetsAreConfigured checks that TargetFor returns a configured
// target and misses an unconfigured language.
func TestEventTargetsAreConfigured(t *testing.T) {
	cfg, err := loadManifest(t, `events:
  targets:
    - lang: go
      out: ./gen/events
`)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := cfg.Events.TargetFor(LangGo)
	if !ok || target.Out != "./gen/events" {
		t.Errorf("go target = %+v (%v)", target, ok)
	}
	if _, ok := cfg.Events.TargetFor("rust"); ok {
		t.Error("TargetFor must miss an unconfigured language")
	}
}

// TestRemovedKeysAreRejected checks that a removed key fails the load, naming
// the key and what took its place.
func TestRemovedKeysAreRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"design source", "design:\n  from: ../contracts\n  root: ..\n",
			"design is no longer a manifest key - a manifest holds its own design folder"},
		{"service selection", "output:\n  services: [shop.Orders]\n",
			"output.services is no longer a manifest key - a project generates every service"},
		{"consume middleware", "output:\n  consumeMiddleware: ./internal/consume\n",
			"output.consumeMiddleware is no longer a manifest key - middleware is installed on the bus"},
		{"asyncapi", "events:\n  asyncapi: ./docs/asyncapi.yaml\n",
			"events.asyncapi is no longer a manifest key - craftgo writes no asyncapi document"},
		{"target layout", "events:\n  targets:\n    - lang: go\n      out: ./internal/events\n      layout:\n        types: ./gen/types\n",
			"events.targets[0].layout is no longer a manifest key - the go target places its artefacts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadManifest(t, c.body)
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("err = %v, want one starting %q", err, c.want)
			}
		})
	}
}

func TestEventTargetEnabled(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"./internal/events", true},
		{"-", false},
		{"", false},
	}
	for _, c := range cases {
		if got := (EventTarget{Lang: LangGo, Out: c.out}).Enabled(); got != c.want {
			t.Errorf("Enabled(%q) = %v, want %v", c.out, got, c.want)
		}
	}
}

func TestEventTargetValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		msg  string
	}{
		{
			name: "unknown language",
			body: "events:\n  targets:\n    - lang: cobol\n      out: ./x\n",
			msg:  "not supported",
		},
		{
			name: "missing language",
			body: "events:\n  targets:\n    - out: ./x\n",
			msg:  "missing `lang`",
		},
		{
			name: "duplicate language",
			body: "events:\n  targets:\n    - lang: go\n      out: ./a\n    - lang: go\n      out: ./b\n",
			msg:  "twice",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadManifest(t, c.body)
			if err == nil || !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("err = %v, want it to mention %q", err, c.msg)
			}
		})
	}
}
