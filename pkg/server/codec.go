package server

import (
	"encoding/json"
	"io"
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
