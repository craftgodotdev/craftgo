package server

import (
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// ParseUnsigned and ParseFloat parse to the width of T, a defined type included, and fail
// with strconv's error past it.
func TestParseUnsignedAndFloat(t *testing.T) {
	type port uint16
	if v, err := ParseUnsigned[port]("8080"); err != nil || v != 8080 {
		t.Errorf("ParseUnsigned[port](8080) = %v, %v", v, err)
	}
	if v, err := ParseUnsigned[uint64]("18446744073709551615"); err != nil || v != math.MaxUint64 {
		t.Errorf("ParseUnsigned[uint64](max) = %v, %v", v, err)
	}
	if _, err := ParseUnsigned[uint8]("256"); !errors.Is(err, strconv.ErrRange) {
		t.Errorf("ParseUnsigned[uint8](256) err = %v, want ErrRange", err)
	}
	if _, err := ParseUnsigned[uint32]("-1"); !errors.Is(err, strconv.ErrSyntax) {
		t.Errorf("ParseUnsigned[uint32](-1) err = %v, want ErrSyntax", err)
	}
	if v, err := ParseFloat[float32]("1.5"); err != nil || v != 1.5 {
		t.Errorf("ParseFloat[float32](1.5) = %v, %v", v, err)
	}
	if _, err := ParseFloat[float32]("1e39"); !errors.Is(err, strconv.ErrRange) {
		t.Errorf("ParseFloat[float32](1e39) err = %v, want ErrRange", err)
	}
	if v, err := ParseFloat[float64]("1e39"); err != nil || v != 1e39 {
		t.Errorf("ParseFloat[float64](1e39) = %v, %v", v, err)
	}
}

// ParseFloat refuses NaN and infinities, which strconv spells in several ways, at either width.
func TestParseFloatRejectsNonFinite(t *testing.T) {
	for _, s := range []string{"NaN", "nan", "Inf", "+Inf", "-Infinity", "inf"} {
		if v, err := ParseFloat[float64](s); err == nil {
			t.Errorf("ParseFloat[float64](%q) = %v, want an error", s, v)
		}
		if v, err := ParseFloat[float32](s); err == nil {
			t.Errorf("ParseFloat[float32](%q) = %v, want an error", s, v)
		}
	}
	var numErr *strconv.NumError
	if _, err := ParseFloat[float64]("NaN"); !errors.As(err, &numErr) || !strings.Contains(err.Error(), "not a finite number") {
		t.Errorf("ParseFloat(NaN) err = %v, want a *strconv.NumError saying not a finite number", err)
	}
}

// BindValue answers 400 naming the field, the value and its type, not strconv or its error: a
// number past the type's range is out of range, anything else not a valid value of the type.
func TestBindValueMessages(t *testing.T) {
	for _, c := range []struct {
		raw, want string
		bind      func(w http.ResponseWriter, r *http.Request, raw string) bool
	}{
		{"70000", "port: 70000 is out of range for uint16", func(w http.ResponseWriter, r *http.Request, raw string) bool {
			var dst uint16
			return BindValue(w, r, "port", "uint16", raw, &dst, ParseUnsigned[uint16])
		}},
		{"128", "level: 128 is out of range for int8", func(w http.ResponseWriter, r *http.Request, raw string) bool {
			var dst int8
			return BindValue(w, r, "level", "int8", raw, &dst, ParseSigned[int8])
		}},
		{"abc", `level: "abc" is not a valid int8`, func(w http.ResponseWriter, r *http.Request, raw string) bool {
			var dst int8
			return BindValue(w, r, "level", "int8", raw, &dst, ParseSigned[int8])
		}},
		{"0x1p-2", `ratio: "0x1p-2" is not a valid float64`, func(w http.ResponseWriter, r *http.Request, raw string) bool {
			var dst *float64
			return BindValuePtr(w, r, "ratio", "float64", raw, &dst, ParseFloat[float64])
		}},
		{"NaN", `ratio: "NaN" is not a finite number`, func(w http.ResponseWriter, r *http.Request, raw string) bool {
			var dst float64
			return BindValue(w, r, "ratio", "float64", raw, &dst, ParseFloat[float64])
		}},
		{"1e39", "ratio: 1e39 is out of range for float32", func(w http.ResponseWriter, r *http.Request, raw string) bool {
			var dst []float32
			return BindValues(w, r, "ratio", "float32", []string{"1", raw}, &dst, ParseFloat[float32])
		}},
	} {
		w := httptest.NewRecorder()
		if c.bind(w, httptest.NewRequest(http.MethodGet, "/", nil), c.raw) {
			t.Fatalf("%s: bound", c.raw)
		}
		if w.Code != http.StatusBadRequest || w.Body.String() != `{"message":`+strconv.Quote(c.want)+"}\n" {
			t.Errorf("%s: got %d %s, want %q", c.raw, w.Code, w.Body.String(), c.want)
		}
	}
}

// ParseFloat takes a plain decimal number only, none of the other forms strconv reads.
func TestParseFloatTakesDecimalsOnly(t *testing.T) {
	for _, s := range []string{"1", "-1.5", "+2", ".5", "5.", "6.02e23", "1E-3", "007"} {
		if _, err := ParseFloat[float64](s); err != nil {
			t.Errorf("ParseFloat(%q): %v", s, err)
		}
	}
	for _, s := range []string{"0x1p-2", "0X1P2", "1_000.5", "1e", "e5", ".", "-", "1.5x", " 1", "Infinity", ""} {
		if v, err := ParseFloat[float64](s); !errors.Is(err, strconv.ErrSyntax) && !errors.Is(err, errNotFinite) {
			t.Errorf("ParseFloat(%q) = %v, %v; want a syntax error", s, v, err)
		}
	}
}

func TestRequirePresentAbsentWrites400(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if RequirePresent(w, r, false, "sid", "cookie") {
		t.Fatal("RequirePresent(present=false) = true, want false")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestRequirePresentPresentWritesNothing(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if !RequirePresent(w, r, true, "sid", "cookie") {
		t.Fatal("RequirePresent(present=true) = false, want true")
	}
	// The recorder reports 200 until a status is written.
	if w.Code != http.StatusOK {
		t.Errorf("present param wrote status %d, want no write (200 default)", w.Code)
	}
}

func TestCookiePresent(t *testing.T) {
	with := httptest.NewRequest(http.MethodGet, "/", nil)
	with.AddCookie(&http.Cookie{Name: "sid", Value: "abc"})
	if !CookiePresent(with, "sid") {
		t.Error("CookiePresent with the cookie set = false, want true")
	}
	without := httptest.NewRequest(http.MethodGet, "/", nil)
	if CookiePresent(without, "sid") {
		t.Error("CookiePresent with no cookie = true, want false")
	}
}

// HeaderList reads a list header as RFC 9110 writes one: every line, split at commas, each
// element trimmed, empty elements dropped.
func TestHeaderList(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Add("X-Ids", "1, 2")
	r.Header.Add("X-Ids", "3,,\t4 ")
	if got := HeaderList(r, "X-Ids"); !slices.Equal(got, []string{"1", "2", "3", "4"}) {
		t.Errorf("HeaderList = %q, want [1 2 3 4]", got)
	}
	if got := HeaderList(r, "X-None"); got != nil {
		t.Errorf("HeaderList of an absent header = %q, want nil", got)
	}
}
