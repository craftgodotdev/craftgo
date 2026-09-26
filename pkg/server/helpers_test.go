package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/craftgodotdev/craftgo/pkg/log"
)

// observeLogs points log.Default at an observer until the test ends.
func observeLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.InfoLevel)
	prev := log.Default()
	log.SetDefault(log.NewZap(zap.New(core)))
	t.Cleanup(func() { log.SetDefault(prev) })
	return logs
}

// fakeStatusError is a StatusError with a fixed message and status.
type fakeStatusError struct {
	msg    string
	status int
}

func (e fakeStatusError) Error() string   { return e.msg }
func (e fakeStatusError) HTTPStatus() int { return e.status }

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	return string(out)
}
