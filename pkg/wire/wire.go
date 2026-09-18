// Package wire carries the values a codec must hand through instead of
// encoding: bytes that already ARE the value, in the format the message
// travels in.
//
// It is its own module and imports nothing but the standard library, so a
// generated contract package can name [Raw] and inherit nothing else -
// not the event runtime, and not the craftgo toolchain that generated it.
package wire

import "errors"

// Raw is a value already encoded in the message's own format. A field
// declared `bytes @format(raw)` in a design lowers to this type, and
// every codec passes it through as the bytes of that value in the format
// it speaks: the JSON codec through [Raw.MarshalJSON] and
// [Raw.UnmarshalJSON] below, another codec by registering its own
// handling for this type. Nothing here reads the bytes, so what a
// producer wrote is what a consumer gets - an explicit `null`, an integer
// past 2^53 and a trailing zero such as `1.50` all survive a round trip
// that decoding into `any` would flatten.
//
// A codec proves it honours that with the suite in
// github.com/craftgodotdev/craftgo/pkg/wire/codectest.
//
// What travels unchanged is the VALUE, not the byte stream around it:
// encoding/json compacts what a Marshaler returns, so insignificant
// whitespace between tokens is dropped. Every token, and the text of
// every number and string, is what arrived.
//
// A nil Raw is the absent value; in JSON it encodes as `null`. The four
// bytes `null` are a value in their own right and stay distinguishable
// from absence on the slice's own nil, so a raw field is a Raw in every
// shape and no pointer form exists: `?` only adds `,omitempty` so an
// absent value is omitted, `@nullable` keeps the key. A `*Raw` would
// lose the difference - encoding/json nils the pointer on a JSON `null`
// without ever calling [Raw.UnmarshalJSON]. A zero-length non-nil Raw is
// not a value at all: encoding one reports the error encoding/json
// raises for empty Marshaler output rather than inventing a value it was
// never given.
type Raw []byte

// MarshalJSON returns the bytes verbatim, so the value reaches the wire
// exactly as it was stored. A nil Raw encodes as JSON `null`.
func (r Raw) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	return r, nil
}

// UnmarshalJSON stores a copy of the bytes that arrived, whatever they
// spell - a literal `null` included, which is a JSON value like any
// other and is kept rather than collapsed into absence. One copy is all
// it makes: the decoder's buffer is reused the moment the call returns,
// so aliasing it is not an option.
func (r *Raw) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.New("wire.Raw: UnmarshalJSON on nil pointer")
	}
	*r = append((*r)[:0], data...)
	return nil
}
