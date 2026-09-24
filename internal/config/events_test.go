package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestEventsDefaultToASingleGoTarget checks that a manifest without events
// gets one Go target in ./internal/events.
func TestEventsDefaultToASingleGoTarget(t *testing.T) {
	cfg, err := Load(writeManifest(t, "openapi:\n  title: X\n"))
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
	cfg, err := Load(writeManifest(t, `events:
  targets:
    - lang: go
      out: ./gen/events
`))
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

// TestRemovedKeysAreRejected checks that each removed key fails Load with an
// error naming it.
func TestRemovedKeysAreRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
		key  string
	}{
		{"design source", "design:\n  from: ../contracts\n  root: ..\n", "design"},
		{"service selection", "output:\n  services: [shop.Orders]\n", "output.services"},
		{"consume middleware", "output:\n  consumeMiddleware: ./internal/consume\n", "output.consumeMiddleware"},
		{"asyncapi", "events:\n  asyncapi: ./docs/asyncapi.yaml\n", "events.asyncapi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(writeManifest(t, c.body))
			if err == nil {
				t.Fatalf("%s was accepted", c.key)
			}
			if !strings.Contains(err.Error(), c.key) {
				t.Errorf("error does not name the key: %v", err)
			}
		})
	}
}

// TestTargetLayoutIsRejected checks that a target's `layout:` fails Load.
func TestTargetLayoutIsRejected(t *testing.T) {
	_, err := Load(writeManifest(t, `events:
  targets:
    - lang: go
      out: ./internal/events
      layout:
        types: ./gen/types
`))
	if err == nil {
		t.Fatal("a layout on a target that reads none must be rejected")
	}
	if !strings.Contains(err.Error(), "layout") {
		t.Errorf("error does not name the key: %v", err)
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
			_, err := Load(writeManifest(t, c.body))
			if err == nil || !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("err = %v, want it to mention %q", err, c.msg)
			}
		})
	}
}
