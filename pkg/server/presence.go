package server

import (
	"bytes"
)

// MissingMember returns the validation error "<path>: required" for a required member a JSON body
// left out or sent as null, path being the member's JSON path.
func MissingMember(path string) error { return &missingMemberError{path: path} }

// missingMemberError is the [MissingMember] error.
type missingMemberError struct{ path string }

func (e *missingMemberError) Error() string { return e.path + ": required" }

// Nullable holds a JSON member that may be null, telling an absent member from a null one: a
// generated handler decodes a @nullable member into it. The value decodes with the
// codec [JSON] returns, so a codec installed with [SetGlobalJSONCodec] must call a member's
// json.Unmarshaler.
type Nullable[T any] struct {
	value   T
	present bool
	null    bool
}

// nullLiteral is JSON's null.
var nullLiteral = []byte("null")

// UnmarshalJSON records that the member came and decodes it unless it is null. A value that fails
// to decode fails with a *memberError, which the default codec words at the value's JSON path.
func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.present = true
	if bytes.Equal(bytes.TrimSpace(b), nullLiteral) {
		n.null = true
		return nil
	}
	if err := JSON().Decode(bytes.NewReader(b), &n.value); err != nil {
		return &memberError{err: err}
	}
	return nil
}

// memberError is the failure of a [Nullable]'s value to decode, the codec's error, which names
// its path from the value.
type memberError struct{ err error }

func (e *memberError) Error() string { return e.err.Error() }
func (e *memberError) Unwrap() error { return e.err }

// Present reports whether the member came, null or not.
func (n *Nullable[T]) Present() bool { return n.present }

// Value returns the decoded value, or nil when the member was absent or null.
func (n *Nullable[T]) Value() *T {
	if !n.present || n.null {
		return nil
	}
	return &n.value
}
