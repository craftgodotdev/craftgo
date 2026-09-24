package server

import (
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
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

// BindValue with ParseUnsigned answers 400 naming the field for a value out of range.
func TestBindValueUnsignedOutOfRange(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	var dst uint16
	if BindValue(w, r, "port", "uint16", "70000", &dst, ParseUnsigned[uint16]) {
		t.Fatal("BindValue accepted 70000 for a uint16")
	}
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "port: invalid uint16 value") {
		t.Errorf("status %d, body %q", w.Code, w.Body.String())
	}
	if dst != 0 {
		t.Errorf("dst = %d, want it unchanged", dst)
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
