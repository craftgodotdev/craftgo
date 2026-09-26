package log

import (
	"context"
	"errors"
	"testing"
)

// Every Discard method is a no-op, and Enabled reports false.
func TestDiscardSwallowsEverything(t *testing.T) {
	d := Discard()
	d.Debug("d")
	d.Info("i", String("k", "v"))
	d.Warn("w")
	d.Error("e", Err(errors.New("boom")))
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
