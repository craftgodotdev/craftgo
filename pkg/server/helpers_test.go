package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
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

// gunzip returns b gzip-decoded.
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

// resetCodec restores the default codec and lenient decoding when the test ends.
func resetCodec(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_ = SetStrictJSON(false)
		_ = SetGlobalJSONCodec(nil)
	})
}

// readBody returns a handler that reads the whole request body into *got and fails t on a read
// error.
func readBody(t *testing.T, got *string) http.Handler {
	return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read the request body: %v", err)
		}
		*got = string(b)
	})
}
