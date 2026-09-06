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

// JSONCodec is the small surface generated handlers and the access-log
// middleware delegate to when they need to (de)serialise JSON. The
// default implementation wraps `encoding/json`; production projects can
// substitute sonic, jsoniter, or any compatible alternative through
// [SetGlobalJSONCodec]. A codec that also implements [StrictDecoder] can
// serve a project with `server.strictJSON` on.
type JSONCodec interface {
	Encode(w io.Writer, v any) error
	Decode(r io.Reader, v any) error
}

// StrictDecoder is the optional half of a codec that can honour
// [SetStrictJSON]: DecodeStrict is Decode that rejects an unknown field
// (`<field>: unknown field`) and data after the JSON value (see
// [TrailingData]). The built-in codec implements it.
type StrictDecoder interface {
	DecodeStrict(r io.Reader, v any) error
}

// defaultCodec is the `encoding/json`-backed JSONCodec used unless the
// project installs another one.
type defaultCodec struct{}

// Encode writes v to w as JSON using the standard library encoder.
func (defaultCodec) Encode(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }

// Decode reads v from r as JSON, ignoring unknown fields and anything
// after the value, as encoding/json does.
func (defaultCodec) Decode(r io.Reader, v any) error { return json.NewDecoder(r).Decode(v) }

// DecodeStrict reads v from r, failing on an unknown field with
// `<field>: unknown field` and on anything after the JSON value.
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

// unknownFieldOf extracts the field name from encoding/json's
// `json: unknown field "name"` error, the only form it reports it in.
func unknownFieldOf(err error) (string, bool) {
	const prefix = `json: unknown field `
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return "", false
	}
	name, uerr := strconv.Unquote(msg[len(prefix):])
	return name, uerr == nil
}

// TrailingData fails when anything but whitespace follows a decoded JSON
// value: first in the decoder's read-ahead (buffered), then on the rest
// of the body. A codec's DecodeStrict calls it after decoding, so every
// codec reports leftover data the same way. Reading byte by byte keeps
// the check allocation-free; on a well-formed request the remainder is
// empty or a newline.
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
// what [JSON] hands out: base itself, or base behind [strictCodec].
type codecHolder struct {
	base   JSONCodec
	strict bool
	active JSONCodec
}

// globalJSON is the process-wide codec used by generated transport
// handlers, health endpoints, and any package that calls [JSON]. It is
// swappable via [SetGlobalJSONCodec] / [Server.SetJSONCodec] so a
// project can drop in sonic / jsoniter once at startup and have every
// handler pick up the change without per-handler plumbing. Stored in an
// atomic.Value so concurrent reads during handler dispatch are safe
// against the (rare) runtime swap.
var globalJSON atomic.Value

func init() { globalJSON.Store(codecHolder{base: defaultCodec{}, active: defaultCodec{}}) }

func currentCodec() codecHolder { return globalJSON.Load().(codecHolder) }

// installCodec stores base with the strict switch, refusing a strict
// setting base cannot honour.
func installCodec(base JSONCodec, strict bool) error {
	h := codecHolder{base: base, strict: strict, active: base}
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

// SetGlobalJSONCodec installs c as the codec returned by [JSON]; nil
// restores the built-in one. Call once at startup before serving
// traffic; the swap itself is goroutine-safe but in-flight handlers
// already mid-encode keep using the codec they captured. While strict
// JSON is on, c must implement [StrictDecoder], or the call fails and the
// previous codec stays installed.
func SetGlobalJSONCodec(c JSONCodec) error {
	if c == nil {
		c = defaultCodec{}
	}
	return installCodec(c, currentCodec().strict)
}

// SetStrictJSON selects how a request body that does not match the
// request type exactly is treated. Strict rejects an unknown field and
// data after the JSON value; lenient, the default, ignores both as
// encoding/json does. Strict needs the installed codec to implement
// [StrictDecoder], or the call fails and the setting stays as it was.
func SetStrictJSON(strict bool) error {
	return installCodec(currentCodec().base, strict)
}

// JSON returns the codec currently in effect: the one installed via
// [SetGlobalJSONCodec] (or the stdlib default when none has been set),
// decoding strictly while [SetStrictJSON] is on. Generated handlers call
// this every time they need to (de)serialise so a runtime swap takes
// effect on the next request.
func JSON() JSONCodec { return currentCodec().active }
