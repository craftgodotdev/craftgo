package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
)

// contentTypeJSON is the Content-Type of the framework's JSON responses.
const contentTypeJSON = "application/json; charset=utf-8"

// JSONCodec encodes and decodes the framework's JSON; install one with [SetGlobalJSONCodec].
type JSONCodec interface {
	Encode(w io.Writer, v any) error
	Decode(r io.Reader, v any) error
}

// StrictDecoder is the optional codec method [SetStrictJSON] needs: DecodeStrict decodes like
// Decode but fails on an unknown field ("<field>: unknown field") and on data after the JSON
// value (see [TrailingData]).
type StrictDecoder interface {
	DecodeStrict(r io.Reader, v any) error
}

// defaultCodec is the encoding/json codec.
type defaultCodec struct{}

func (defaultCodec) Encode(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }

// Decode ignores unknown fields and anything after the value.
func (defaultCodec) Decode(r io.Reader, v any) error { return json.NewDecoder(r).Decode(v) }

func (defaultCodec) DecodeStrict(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if field, ok := unknownFieldOf(err); ok {
			return fmt.Errorf("%s: unknown field", field)
		}
		return err
	}
	return TrailingData(dec.Buffered(), r)
}

// unknownFieldOf extracts the name from encoding/json's `json: unknown field "name"` error.
func unknownFieldOf(err error) (string, bool) {
	const prefix = `json: unknown field `
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return "", false
	}
	name, uerr := strconv.Unquote(msg[len(prefix):])
	return name, uerr == nil
}

// TrailingData fails when anything but JSON whitespace remains in buffered (a decoder's
// read-ahead) or rest; a [StrictDecoder] calls it after decoding a value.
func TrailingData(buffered, rest io.Reader) error {
	var b [1]byte
	for _, src := range [...]io.Reader{buffered, rest} {
		for {
			n, err := src.Read(b[:])
			if n == 1 && !isJSONSpace(b[0]) {
				return errors.New("body: unexpected data after the JSON value")
			}
			if err != nil {
				break
			}
		}
	}
	return nil
}

func isJSONSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// strictCodec is the installed codec with Decode routed through its
// DecodeStrict.
type strictCodec struct {
	JSONCodec
	strict StrictDecoder
}

func (c strictCodec) Decode(r io.Reader, v any) error { return c.strict.DecodeStrict(r, v) }

// codecHolder is the installed codec plus the strict switch. active is
// what [JSON] hands out: base itself, or base behind strictCodec.
type codecHolder struct {
	base   JSONCodec
	strict bool
	active JSONCodec
}

// globalJSON holds the current codecHolder.
var globalJSON atomic.Pointer[codecHolder]

func init() { globalJSON.Store(&codecHolder{base: defaultCodec{}, active: defaultCodec{}}) }

func currentCodec() *codecHolder { return globalJSON.Load() }

// installCodec stores base with the strict switch, refusing a strict
// setting base cannot honour.
func installCodec(base JSONCodec, strict bool) error {
	h := &codecHolder{base: base, strict: strict, active: base}
	if strict {
		sd, ok := base.(StrictDecoder)
		if !ok {
			return fmt.Errorf("strict JSON is on but codec %T has no DecodeStrict", base)
		}
		h.active = strictCodec{JSONCodec: base, strict: sd}
	}
	globalJSON.Store(h)
	return nil
}

// SetGlobalJSONCodec installs c as the codec [JSON] returns; nil restores the built-in one.
// While strict JSON is on, c must implement [StrictDecoder], or the call fails and changes nothing.
func SetGlobalJSONCodec(c JSONCodec) error {
	if c == nil {
		c = defaultCodec{}
	}
	return installCodec(c, currentCodec().strict)
}

// SetStrictJSON switches strict decoding, off by default: while on, [JSON] decodes through
// the codec's [StrictDecoder]. Turning it on fails, changing nothing, when the codec has none.
func SetStrictJSON(strict bool) error {
	return installCodec(currentCodec().base, strict)
}

// JSON returns the codec in effect: the one [SetGlobalJSONCodec] installed, encoding/json by
// default, decoding strictly while [SetStrictJSON] is on.
func JSON() JSONCodec { return currentCodec().active }
