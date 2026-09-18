// Package codecjson provides a JSON [events.Codec]. `pkg/events` has no
// default encoding; a project installs one on the bus.
//
// A `bytes @format(raw)` field travels through it untouched: the bytes
// are already JSON, so they are embedded where the value belongs. The
// conformance suite for that contract is `pkg/wire/codectest`, and this
// codec is held to it from the e2e fixture, which is the module where
// both the event runtime and the craftgo root module are in scope.
package codecjson

import "encoding/json"

// Codec encodes payloads with encoding/json.
type Codec struct{}

// Name returns "json", the value stamped into [events.MetaCodec].
func (Codec) Name() string { return "json" }

// Marshal encodes v as JSON.
func (Codec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

// Unmarshal decodes JSON into v.
func (Codec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
