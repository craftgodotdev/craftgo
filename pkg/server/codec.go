package server

import (
	"encoding/json"
	"io"
	"sync/atomic"
)

// JSONCodec is the small surface generated handlers and the access-log
// middleware delegate to when they need to (de)serialise JSON. The
// default implementation wraps `encoding/json`; production projects can
// substitute sonic, jsoniter, or any compatible alternative.
type JSONCodec interface {
	Encode(w io.Writer, v any) error
	Decode(r io.Reader, v any) error
}

// defaultCodec is a `encoding/json`-backed JSONCodec used unless the
// project calls SetJSONCodec.
type defaultCodec struct{}

// Encode writes v to w as JSON using the standard library encoder.
func (defaultCodec) Encode(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }

// Decode reads v from r as JSON using the standard library decoder.
func (defaultCodec) Decode(r io.Reader, v any) error { return json.NewDecoder(r).Decode(v) }

// codecHolder boxes the interface so every atomic.Value.Store sees the
// same concrete type (atomic.Value rejects type-mismatched stores).
type codecHolder struct{ c JSONCodec }

// globalJSON is the process-wide codec used by generated transport
// handlers, health endpoints, and any package that calls [JSON]. It is
// swappable via [SetGlobalJSONCodec] / [Server.SetJSONCodec] so a
// project can drop in sonic / jsoniter once at startup and have every
// handler pick up the change without per-handler plumbing. Stored in an
// atomic.Value so concurrent reads during handler dispatch are safe
// against the (rare) runtime swap.
var globalJSON atomic.Value

func init() { globalJSON.Store(codecHolder{c: defaultCodec{}}) }

// SetGlobalJSONCodec installs c as the codec returned by [JSON]. Call
// once at startup before serving traffic; the swap itself is
// goroutine-safe but in-flight handlers already mid-encode keep using
// the codec they captured.
func SetGlobalJSONCodec(c JSONCodec) {
	if c == nil {
		c = defaultCodec{}
	}
	globalJSON.Store(codecHolder{c: c})
}

// JSON returns the codec currently installed via [SetGlobalJSONCodec]
// (or the stdlib default when none has been set). Generated handlers
// call this every time they need to (de)serialise so a runtime swap
// takes effect on the next request.
func JSON() JSONCodec { return globalJSON.Load().(codecHolder).c }
