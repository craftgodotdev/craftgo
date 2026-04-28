package main

import (
	"net/http"
	"strings"
	"testing"
)

// validators_test.go is the comprehensive validator matrix. The smoke
// tests in smoke_test.go cover the v1 catalogue at a high level
// (ValidateCalc / Tags / Formats / Page / Enum); this file fills in
// per-validator and per-shape coverage:
//
//   - Single*  — one validator per field, every kind exercised in isolation.
//   - Mixed    — multiple validators stacked on one field, ordering pinned.
//   - Defaults — @default values pre-filled before decode.
//   - Generic  — runtime type-assertion through Page<Item>.
//
// Each block has at least one happy path and one rejection per validator
// so a regression in any single emit function fails a focused test.

// ============================================================================
// SINGLE — one validator per field
// ============================================================================

// ----- string validators ---------------------------------------------------

func TestSingleString_Required(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// Missing `name` → @required fails.
	status, body := postJSON(t, ts, "/api/v1/validate/single/string", map[string]any{
		"bounded": "abcd",
		"minOnly": "abc",
		"maxOnly": "abc",
		"alpha":   "abc",
		"email":   "x@y.z",
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "name: required") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleString_LengthBounds(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// `bounded` length 2 (below @length(3,10)).
	status, body := postJSON(t, ts, "/api/v1/validate/single/string", map[string]any{
		"name": "n", "bounded": "ab", "minOnly": "abc", "maxOnly": "abc", "alpha": "abc", "email": "x@y.z",
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "bounded: length out of range") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleString_MinLength(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/string", map[string]any{
		"name": "n", "bounded": "abcd", "minOnly": "a", "maxOnly": "abc", "alpha": "abc", "email": "x@y.z",
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "minOnly: length less than 2") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleString_MaxLength(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/string", map[string]any{
		"name": "n", "bounded": "abcd", "minOnly": "abc",
		"maxOnly": "abcdefghi", "alpha": "abc", "email": "x@y.z",
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "maxOnly: length greater than 8") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleString_Pattern(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/string", map[string]any{
		"name": "n", "bounded": "abcd", "minOnly": "abc",
		"maxOnly": "abc", "alpha": "ABC", "email": "x@y.z",
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "alpha: does not match pattern") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleString_Format(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/string", map[string]any{
		"name": "n", "bounded": "abcd", "minOnly": "abc",
		"maxOnly": "abc", "alpha": "abc", "email": "not-an-email",
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "email: not a valid email") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleString_HappyPath(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/string", map[string]any{
		"name": "n", "bounded": "abcd", "minOnly": "abc",
		"maxOnly": "abc", "alpha": "abc", "email": "x@y.z",
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("got %d %s", status, body)
	}
}

// ----- numeric validators --------------------------------------------------

func TestSingleNumeric_Min(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/numeric", map[string]any{
		"minVal": -1, "maxVal": 50, "inRange": 50, "pos": 1, "neg": -1, "multOf5": 5,
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "minVal: below minimum 0") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleNumeric_Max(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/numeric", map[string]any{
		"minVal": 0, "maxVal": 200, "inRange": 50, "pos": 1, "neg": -1, "multOf5": 5,
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "maxVal: above maximum 100") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleNumeric_Range(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/numeric", map[string]any{
		"minVal": 0, "maxVal": 50, "inRange": 200, "pos": 1, "neg": -1, "multOf5": 5,
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "inRange: out of range [1, 99]") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleNumeric_Positive(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/numeric", map[string]any{
		"minVal": 0, "maxVal": 50, "inRange": 50, "pos": 0, "neg": -1, "multOf5": 5,
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "pos: must be positive") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleNumeric_Negative(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/numeric", map[string]any{
		"minVal": 0, "maxVal": 50, "inRange": 50, "pos": 1, "neg": 0, "multOf5": 5,
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "neg: must be negative") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleNumeric_MultipleOf(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/numeric", map[string]any{
		"minVal": 0, "maxVal": 50, "inRange": 50, "pos": 1, "neg": -1, "multOf5": 7,
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "multOf5: must be a multiple of 5") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleNumeric_HappyPath(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/numeric", map[string]any{
		"minVal": 0, "maxVal": 100, "inRange": 50, "pos": 1, "neg": -1, "multOf5": 25,
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("got %d %s", status, body)
	}
}

// ----- array validators ----------------------------------------------------

func TestSingleArray_MinItems(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/array", map[string]any{
		"atLeastOne": []string{}, "atMostFive": []string{"a"}, "distinct": []string{"a"},
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "atLeastOne: minItems 1") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleArray_MaxItems(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/array", map[string]any{
		"atLeastOne": []string{"a"},
		"atMostFive": []string{"a", "b", "c", "d", "e", "f"},
		"distinct":   []string{"a"},
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "atMostFive: maxItems 5") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleArray_UniqueItems(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/array", map[string]any{
		"atLeastOne": []string{"a"}, "atMostFive": []string{"a"},
		"distinct": []string{"a", "b", "a"},
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "distinct: items must be unique") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestSingleArray_HappyPath(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/single/array", map[string]any{
		"atLeastOne": []string{"a"},
		"atMostFive": []string{"a", "b"},
		"distinct":   []string{"a", "b"},
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("got %d %s", status, body)
	}
}

// ============================================================================
// MIXED — multiple validators per field
// ============================================================================

func TestMixed_HappyPath(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/mixed", map[string]any{
		"username": "harry",
		"score":    50,
		"tags":     []string{"a", "b"},
		"contact":  "x@y.z",
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("got %d %s", status, body)
	}
}

func TestMixed_UsernameRequiredFiresFirst(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// Empty username → @required fires before @length / @pattern.
	status, body := postJSON(t, ts, "/api/v1/validate/mixed", map[string]any{
		"username": "", "score": 50, "tags": []string{"a"},
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "username: required") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestMixed_UsernameLengthBeforePattern(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// "ab" passes pattern but fails @length(3, 20) — length runs first.
	status, body := postJSON(t, ts, "/api/v1/validate/mixed", map[string]any{
		"username": "ab", "score": 50, "tags": []string{"a"},
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "username: length out of range") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestMixed_UsernamePatternRejectsUpper(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// Length OK, pattern rejects uppercase.
	status, body := postJSON(t, ts, "/api/v1/validate/mixed", map[string]any{
		"username": "Alice", "score": 50, "tags": []string{"a"},
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "username: does not match pattern") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestMixed_ScoreMultipleOf(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// Score 7 in range but not a multiple of 5.
	status, body := postJSON(t, ts, "/api/v1/validate/mixed", map[string]any{
		"username": "harry", "score": 7, "tags": []string{"a"},
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "score: must be a multiple of 5") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestMixed_TagsCombined(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// Duplicate tags — minItems / maxItems pass, uniqueItems fails.
	status, body := postJSON(t, ts, "/api/v1/validate/mixed", map[string]any{
		"username": "harry", "score": 50, "tags": []string{"a", "a"},
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "tags: items must be unique") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestMixed_OptionalContactSkipsWhenAbsent(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// `contact` omitted — nil-guarded format check skips.
	status, body := postJSON(t, ts, "/api/v1/validate/mixed", map[string]any{
		"username": "harry", "score": 50, "tags": []string{"a"},
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("got %d %s", status, body)
	}
}

func TestMixed_OptionalContactValidatesWhenPresent(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/mixed", map[string]any{
		"username": "harry", "score": 50, "tags": []string{"a"}, "contact": "bogus",
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "contact: not a valid email") {
		t.Errorf("got %d %s", status, body)
	}
}

// ============================================================================
// DEFAULTS — pre-fill before decode + validate
// ============================================================================

func TestDefaults_AllOmitted(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// Empty body — every field falls back to its @default.
	// Defaults: status=pending, limit=20, active=true, ratio=0.5
	// All defaults satisfy the validators.
	status, body := postJSON(t, ts, "/api/v1/validate/defaults", map[string]any{})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("got %d %s", status, body)
	}
}

func TestDefaults_ExplicitOverrideKept(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// Explicit values override defaults — JSON decoder fills.
	status, body := postJSON(t, ts, "/api/v1/validate/defaults", map[string]any{
		"status": "active",
		"limit":  50,
		"active": false,
		"ratio":  1.0,
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("got %d %s", status, body)
	}
}

func TestDefaults_ExplicitInvalidStillRejects(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// `limit: 0` overrides default 20 → @range(1,100) rejects.
	status, body := postJSON(t, ts, "/api/v1/validate/defaults", map[string]any{
		"limit": 0,
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "limit: out of range") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestDefaults_ExplicitNegativeRejectsRatio(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// `ratio: -1` overrides default 0.5 → @min(0) rejects.
	status, body := postJSON(t, ts, "/api/v1/validate/defaults", map[string]any{
		"ratio": -1,
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "ratio: below minimum") {
		t.Errorf("got %d %s", status, body)
	}
}

// ============================================================================
// CROSS-FIELD — @requiresOneOf + @mutuallyExclusive
// ============================================================================

// TestCrossField_RequiresOneOfRejectsBothEmpty verifies the
// @requiresOneOf rule fires when neither email nor phone is supplied.
func TestCrossField_RequiresOneOfRejectsBothEmpty(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/cross", map[string]any{})
	if status != http.StatusBadRequest || !strings.Contains(body, "requiresOneOf") {
		t.Errorf("got %d %s", status, body)
	}
}

// TestCrossField_RequiresOneOfAcceptsEither happy path: providing one
// of the two channels is enough.
func TestCrossField_RequiresOneOfAcceptsEither(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/cross", map[string]any{
		"email": "x@y.z",
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("got %d %s", status, body)
	}
}

// TestCrossField_MutuallyExclusiveRejectsBoth verifies the
// @mutuallyExclusive rule fires when both legacy toggles are true.
func TestCrossField_MutuallyExclusiveRejectsBoth(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/cross", map[string]any{
		"email":     "x@y.z",
		"sms":       true,
		"voicemail": true,
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "mutuallyExclusive") {
		t.Errorf("got %d %s", status, body)
	}
}

// ============================================================================
// METHOD LIMITS — @readTimeout + @maxBodySize
// ============================================================================

// TestLimited_RejectsOversizedBody pins @maxBodySize: a request body
// over the cap (1024 bytes) trips http.MaxBytesReader before
// json.Decode runs, so the handler reports a 400 with a body-size
// error instead of decoding successfully.
func TestLimited_RejectsOversizedBody(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// Build a tags array large enough to push the JSON body past 1KB.
	big := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		big = append(big, "tag-with-padding-just-to-grow-the-body")
	}
	status, body := postJSON(t, ts, "/api/v1/validate/limited", map[string]any{
		"tags": big,
	})
	if status >= 200 && status < 300 {
		t.Errorf("expected 4xx for oversized body, got %d %s", status, body)
	}
}

// TestLimited_AcceptsSmallBody pins the negative case: a tiny body
// passes through both the size cap and the validators.
func TestLimited_AcceptsSmallBody(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/limited", map[string]any{
		"tags": []string{"a", "b"},
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("got %d %s", status, body)
	}
}

// ============================================================================
// GENERIC — Page<GenericItem> validates each item via type-assertion
// ============================================================================

func TestGenericPageItems_HappyPath(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/generic", map[string]any{
		"page": map[string]any{
			"items": []map[string]any{
				{"label": "alpha", "qty": 5},
				{"label": "beta", "qty": 10},
			},
			"total": 2,
		},
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("got %d %s", status, body)
	}
}

func TestGenericPageItems_InnerRequiredFires(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// Empty label on second item → inner @required must fire.
	status, body := postJSON(t, ts, "/api/v1/validate/generic", map[string]any{
		"page": map[string]any{
			"items": []map[string]any{
				{"label": "alpha", "qty": 5},
				{"label": "", "qty": 1},
			},
		},
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "label: required") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestGenericPageItems_InnerPositiveFires(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// qty: 0 → inner @positive rejects.
	status, body := postJSON(t, ts, "/api/v1/validate/generic", map[string]any{
		"page": map[string]any{
			"items": []map[string]any{{"label": "alpha", "qty": 0}},
		},
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "qty: must be positive") {
		t.Errorf("got %d %s", status, body)
	}
}

func TestGenericPageItems_EmptyArrayPasses(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	// No items → inner Validate is never invoked, request accepted.
	status, body := postJSON(t, ts, "/api/v1/validate/generic", map[string]any{
		"page": map[string]any{"items": []map[string]any{}},
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("got %d %s", status, body)
	}
}
