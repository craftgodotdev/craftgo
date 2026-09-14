// Package codecjson provides a JSON [events.Codec]. `pkg/events` has no
// default encoding; a project installs one on the bus.
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
