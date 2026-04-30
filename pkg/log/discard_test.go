package log

import (
	"context"
	"errors"
	"testing"
)

// TestDiscardSwallowsEverything pins the contract: every Logger method
// must work, must not panic, and must not produce output when called
// on the discard sink. Verified by the absence of stdout/stderr
// observed by the test runner.
func TestDiscardSwallowsEverything(t *testing.T) {
	d := Discard()
	d.Debug("d")
	d.Info("i", String("k", "v"))
	d.Warn("w")
	d.Error("e", Err(errors.New("boom")))
	// Context-aware path goes through the WithContext chain — no
	// dedicated `*Ctx` shorthand on the interface.
	d.WithContext(context.Background()).Info("ctx-info")
	if d.With(String("k", "v")) == nil {
		t.Error("With returned nil")
	}
	if d.WithContext(context.Background()) == nil {
		t.Error("WithContext returned nil")
	}
	if d.Enabled(LevelInfo) {
		t.Error("Discard.Enabled should always be false")
	}
}
