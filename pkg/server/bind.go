package server

import (
	"fmt"
	"net/http"
	"strconv"
	"unsafe"
)

// Wire-parse helpers shared by every generated handler: they turn a raw
// HTTP string (query / header / cookie / form value) into a typed field,
// reporting a parse failure through [WriteValidationError]. The codegen
// emits one call per bound field instead of an inline parse block.

// The constraint sets cover every wire-parseable Go kind plus any named
// scalar over one of them (the `~` operator). No stdlib constraint spans
// all sized integers, so they are declared here.
type wireSigned interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}
type wireUnsigned interface {
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}
type wireFloat interface {
	~float32 | ~float64
}

// bitSize reports the bit width of T (8 / 16 / 32 / 64) so the parse
// rejects a value that overflows the declared field type - an `int8`
// field still rejects 300. A named scalar reports its underlying width.
func bitSize[T any]() int {
	var z T
	return int(unsafe.Sizeof(z)) * 8
}

// ParseSigned parses s as a signed integer sized to T and converts to T,
// covering the builtin int kinds and int-backed scalars.
func ParseSigned[T wireSigned](s string) (T, error) {
	n, err := strconv.ParseInt(s, 10, bitSize[T]())
	return T(n), err
}

// ParseUnsigned is the unsigned counterpart of [ParseSigned].
func ParseUnsigned[T wireUnsigned](s string) (T, error) {
	n, err := strconv.ParseUint(s, 10, bitSize[T]())
	return T(n), err
}

// ParseFloat parses s as a float sized to T (float32 / float64).
func ParseFloat[T wireFloat](s string) (T, error) {
	n, err := strconv.ParseFloat(s, bitSize[T]())
	return T(n), err
}

// ParseBool parses s as a bool ("1"/"t"/"true"/... per strconv).
func ParseBool[T ~bool](s string) (T, error) {
	b, err := strconv.ParseBool(s)
	return T(b), err
}

// BindValue parses raw into *dst when raw is non-empty. An absent or
// present-but-empty value (`?x=`) leaves *dst at its zero value. A parse
// failure writes a validation error and returns false so the handler
// returns early.
func BindValue[T any](w http.ResponseWriter, r *http.Request, field, kind, raw string, dst *T, parse func(string) (T, error)) bool {
	if raw == "" {
		return true
	}
	v, err := parse(raw)
	if err != nil {
		WriteValidationError(w, r, fmt.Errorf("%s: invalid %s value: %v", field, kind, err))
		return false
	}
	*dst = v
	return true
}

// BindValuePtr is the optional (`*T`) variant: it points *dst at the
// parsed value, leaving it nil when raw is empty.
func BindValuePtr[T any](w http.ResponseWriter, r *http.Request, field, kind, raw string, dst **T, parse func(string) (T, error)) bool {
	if raw == "" {
		return true
	}
	v, err := parse(raw)
	if err != nil {
		WriteValidationError(w, r, fmt.Errorf("%s: invalid %s value: %v", field, kind, err))
		return false
	}
	*dst = &v
	return true
}

// RequirePresent writes a 400 and returns false when a required wire
// parameter's key is absent. `present` is the source-specific presence
// test the caller computes (url.Values.Has, a non-empty header-values
// slice, ...); a present-but-empty value (`?q=`) counts as present, since
// the value may legitimately be the empty string. Mirrors the BindValue
// contract: the generated handler returns early when this returns false.
func RequirePresent(w http.ResponseWriter, r *http.Request, present bool, field, kind string) bool {
	if !present {
		WriteValidationError(w, r, fmt.Errorf("%s: missing required %s parameter", field, kind))
		return false
	}
	return true
}

// CookiePresent reports whether the named cookie is on the request.
// `r.Cookie` returns http.ErrNoCookie when absent, so a nil error means
// present. Used by the generated handler to drive RequirePresent for a
// required cookie parameter.
func CookiePresent(r *http.Request, name string) bool {
	_, err := r.Cookie(name)
	return err == nil
}

// BindValues parses each element of raw into *dst (repeated `?ids=1&ids=2`
// or a multi-value header). One bad element fails the whole bind.
func BindValues[T any](w http.ResponseWriter, r *http.Request, field, kind string, raw []string, dst *[]T, parse func(string) (T, error)) bool {
	if len(raw) == 0 {
		// Key absent: leave dst as-is so a prefilled `@default` survives.
		return true
	}
	// Key present: the wire array carries the full value, so REPLACE rather
	// than append - appending onto a prefilled `@default` would concatenate
	// the default with the request ([7,8] + [4,5] = [7,8,4,5]).
	*dst = (*dst)[:0]
	for _, s := range raw {
		v, err := parse(s)
		if err != nil {
			WriteValidationError(w, r, fmt.Errorf("%s: invalid %s value: %v", field, kind, err))
			return false
		}
		*dst = append(*dst, v)
	}
	return true
}
