package server

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unsafe"
)

type wireSigned interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}
type wireUnsigned interface {
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}
type wireFloat interface {
	~float32 | ~float64
}

// bitSize returns T's width in bits, so a parse rejects a value that overflows T.
func bitSize[T any]() int {
	var z T
	return int(unsafe.Sizeof(z)) * 8
}

// ParseSigned parses s as a base-10 integer of T's width, failing with strconv's error.
func ParseSigned[T wireSigned](s string) (T, error) {
	n, err := strconv.ParseInt(s, 10, bitSize[T]())
	return T(n), err
}

// ParseUnsigned is the unsigned counterpart of [ParseSigned].
func ParseUnsigned[T wireUnsigned](s string) (T, error) {
	n, err := strconv.ParseUint(s, 10, bitSize[T]())
	return T(n), err
}

// errNotFinite is the reason [ParseFloat] refuses NaN and the infinities.
var errNotFinite = errors.New("not a finite number")

// ParseFloat parses s as a finite float of T's width, failing with strconv's error, or with a
// *strconv.NumError for NaN and the infinities.
func ParseFloat[T wireFloat](s string) (T, error) {
	n, err := strconv.ParseFloat(s, bitSize[T]())
	if err == nil && (math.IsNaN(n) || math.IsInf(n, 0)) {
		return 0, &strconv.NumError{Func: "ParseFloat", Num: s, Err: errNotFinite}
	}
	return T(n), err
}

// ParseBool parses s as a bool ("1"/"t"/"true"/... per strconv).
func ParseBool[T ~bool](s string) (T, error) {
	b, err := strconv.ParseBool(s)
	return T(b), err
}

// writeInvalidValue writes the validation error "<field>: invalid <kind> value: <err>".
func writeInvalidValue(w http.ResponseWriter, r *http.Request, field, kind string, err error) {
	WriteValidationError(w, r, fmt.Errorf("%s: invalid %s value: %v", field, kind, err))
}

// BindValue parses a non-empty raw into *dst; an empty raw leaves *dst unchanged. A parse
// failure writes a validation error with [WriteValidationError] and returns false.
func BindValue[T any](w http.ResponseWriter, r *http.Request, field, kind, raw string, dst *T, parse func(string) (T, error)) bool {
	if raw == "" {
		return true
	}
	v, err := parse(raw)
	if err != nil {
		writeInvalidValue(w, r, field, kind, err)
		return false
	}
	*dst = v
	return true
}

// BindValuePtr is [BindValue] for an optional field: *dst points at the parsed value.
func BindValuePtr[T any](w http.ResponseWriter, r *http.Request, field, kind, raw string, dst **T, parse func(string) (T, error)) bool {
	if raw == "" {
		return true
	}
	v, err := parse(raw)
	if err != nil {
		writeInvalidValue(w, r, field, kind, err)
		return false
	}
	*dst = &v
	return true
}

// RequirePresent returns present, first writing the validation error
// "<field>: missing required <kind> parameter" when it is false.
func RequirePresent(w http.ResponseWriter, r *http.Request, present bool, field, kind string) bool {
	if !present {
		WriteValidationError(w, r, fmt.Errorf("%s: missing required %s parameter", field, kind))
		return false
	}
	return true
}

// HeaderList returns the elements of list header name as RFC 9110 writes them: every line of it,
// split at commas, each element trimmed and empty ones dropped; nil when r carries none.
func HeaderList(r *http.Request, name string) []string {
	var out []string
	for _, line := range r.Header.Values(name) {
		for part := range strings.SplitSeq(line, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// CookiePresent reports whether r carries the named cookie.
func CookiePresent(r *http.Request, name string) bool {
	_, err := r.Cookie(name)
	return err == nil
}

// BindValues replaces *dst with the parsed elements of raw, leaving it unchanged when raw is
// empty. One bad element writes a validation error and returns false.
func BindValues[T any](w http.ResponseWriter, r *http.Request, field, kind string, raw []string, dst *[]T, parse func(string) (T, error)) bool {
	if len(raw) == 0 {
		return true
	}
	*dst = (*dst)[:0]
	for _, s := range raw {
		v, err := parse(s)
		if err != nil {
			writeInvalidValue(w, r, field, kind, err)
			return false
		}
		*dst = append(*dst, v)
	}
	return true
}
