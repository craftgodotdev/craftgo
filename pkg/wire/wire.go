// Package wire holds [Raw], the pre-encoded value codecs pass through. It is a
// standalone module that imports only the standard library.
package wire

import "errors"

// Raw is a value already encoded in the message's format, which codecs carry
// through unchanged (encoding/json still compacts whitespace). A nil Raw is
// absent and encodes as JSON null; the four bytes `null` are a value.
type Raw []byte

// MarshalJSON returns r verbatim, or `null` when r is nil; encoding/json
// rejects the empty output of a non-nil empty Raw.
func (r Raw) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	return r, nil
}

// UnmarshalJSON stores a copy of data, a literal `null` included, reusing r's
// capacity. It fails on a nil receiver.
func (r *Raw) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errors.New("wire.Raw: UnmarshalJSON on nil pointer")
	}
	*r = append((*r)[:0], data...)
	return nil
}
