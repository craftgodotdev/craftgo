package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// RequirePresent guards required wire params (notably cookies, which the binder
// can't tell "absent" from "empty" for otherwise). Absent -> 400 + false;
// present -> true with nothing written.

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
	// httptest recorder reports 200 until something writes a status; a present
	// param must leave the response untouched.
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
