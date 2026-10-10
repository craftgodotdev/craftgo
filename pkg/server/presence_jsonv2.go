//go:build go1.27 && goexperiment.jsonv2

package server

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// UnmarshalJSONFrom is [Nullable.UnmarshalJSON] for encoding/json on its v2 engine, Go 1.27's,
// which passes on the error an UnmarshalJSON returns without the member's path; decoding the
// value on dec, as part of the document, keeps the path in a type error.
func (n *Nullable[T]) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	n.present = true
	if dec.PeekKind() == 'n' {
		n.null = true
		_, err := dec.ReadToken()
		return err
	}
	return jsonv2.UnmarshalDecode(dec, &n.value)
}
