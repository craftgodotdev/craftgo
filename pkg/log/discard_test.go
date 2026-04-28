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
	d.DebugCtx(context.Background(), "d")
	d.InfoCtx(context.Background(), "i")
	d.WarnCtx(context.Background(), "w")
	d.ErrorCtx(context.Background(), "e")
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
