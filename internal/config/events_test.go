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

// A manifest that says nothing about events gets one Go target, so an
// existing project keeps working and a new event lands in the
// conventional place without configuration.
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

// Go is a row in the target list, not a privileged default: a manifest
// states where it lands, and may leave it out entirely.
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

// A manifest naming a key craftgo has removed is told what happened: an
// unknown key is otherwise ignored, so the project would generate
// something other than what the manifest says.
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

// A manifest carrying a target craftgo used to generate is told the
// target was removed, not that it never existed - the generic
// "not supported" list reads as a typo and sends the user looking for
// one.
func TestRemovedLangIsNamedAsRemoved(t *testing.T) {
	_, err := Load(writeManifest(t, `events:
  targets:
    - lang: go
      out: ./internal/events
    - lang: typescript
      out: ./web/src/events
`))
	if err == nil {
		t.Fatal("a removed lang must be rejected")
	}
	for _, want := range []string{"typescript", "was removed", "drop the row"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// No target reads a `layout:`, so the key is rejected rather than
// silently ignored.
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
