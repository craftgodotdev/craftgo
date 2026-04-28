package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dropship-dev/craftgo/example/internal/middleware"
	"github.com/dropship-dev/craftgo/example/internal/routes"
	"github.com/dropship-dev/craftgo/example/svccontext"
	"github.com/dropship-dev/craftgo/pkg/server"
)

// newTestServer wires the same Server + ServiceContext shape the
// production main.go uses, then registers all DSL-generated routes. The
// httptest.Server returned by callers gives end-to-end coverage of the
// handler chain — bind, validate, dispatch, encode — through real HTTP.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	svc := svccontext.NewServiceContext()
	svc.AuthRequired = middleware.NewAuthRequiredMiddleware("Bearer secret-token")
	svc.RateLimit = middleware.NewRateLimitMiddleware(1000, 0)

	srv := server.New(svc)
	routes.RegisterAll(srv, svc)
	return httptest.NewServer(srv.Handler())
}

// postJSON marshals body as JSON, POSTs to ts.URL+path, and returns
// (status, decoded-body-text). The helper is the smallest amount of
// glue that lets the assertions below stay readable.
func postJSON(t *testing.T, ts *httptest.Server, path string, body any) (int, string) {
	t.Helper()
	buf, _ := json.Marshal(body)
	resp, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// TestValidateCalcHappyPath exercises the @positive / @negative /
// @multipleOf validators with conforming values — the request must
// round-trip cleanly.
func TestValidateCalcHappyPath(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/calc", map[string]any{
		"quantity": 5,
		"delta":    -3,
		"pageSize": 20,
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("status = %d, body = %s", status, body)
	}
}

// TestValidateCalcRejectsNonPositive flips `quantity` to a non-positive
// value; the @positive validator must respond 400.
func TestValidateCalcRejectsNonPositive(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/calc", map[string]any{
		"quantity": -1,
		"delta":    -3,
		"pageSize": 20,
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "must be positive") {
		t.Errorf("expected positive-violation message, got %s", body)
	}
}

// TestValidateCalcRejectsNonNegative flips `delta` to a non-negative
// value; @negative must reject.
func TestValidateCalcRejectsNonNegative(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/calc", map[string]any{
		"quantity": 5,
		"delta":    1,
		"pageSize": 20,
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "must be negative") {
		t.Errorf("expected negative-violation message, got %s", body)
	}
}

// TestValidateCalcRejectsMultipleOf flips `pageSize` so it isn't a
// multiple of 10; @multipleOf must reject.
func TestValidateCalcRejectsMultipleOf(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/calc", map[string]any{
		"quantity": 5,
		"delta":    -3,
		"pageSize": 17,
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "multiple of 10") {
		t.Errorf("expected multipleOf message, got %s", body)
	}
}

// TestValidateTagsRejectsDuplicates exercises @uniqueItems — duplicate
// elements must produce a 400.
func TestValidateTagsRejectsDuplicates(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/tags", map[string]any{
		"tags": []string{"a", "b", "a"},
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "unique") {
		t.Errorf("expected uniqueness message, got %s", body)
	}
}

// TestValidateTagsRejectsTooMany covers @maxItems on the same field.
func TestValidateTagsRejectsTooMany(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	tags := []string{}
	for i := 0; i < 11; i++ {
		tags = append(tags, "t"+string(rune('0'+i%10)))
	}
	tags[len(tags)-1] = "extra-unique-tag"
	status, body := postJSON(t, ts, "/api/v1/validate/tags", map[string]any{"tags": tags})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "maxItems") {
		t.Errorf("expected maxItems message, got %s", body)
	}
}

// TestValidateFormatsAcceptsValid submits a fully-conforming request to
// the format endpoint to prove the regex catalogue accepts realistic
// inputs end-to-end (datetime, ipv6, hexcolor, etc.).
func TestValidateFormatsAcceptsValid(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/formats", map[string]any{
		"ipv6":       "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
		"happenedAt": "2026-04-28T12:00:00Z",
		"onDay":      "2026-04-28",
		"atTime":     "12:30:00",
		"cidr":       "192.168.1.0/24",
		"mac":        "01:23:45:67:89:ab",
		"card":       "4111111111111111",
		"blob":       "SGVsbG8=",
		"color":      "#ffaa00",
		"payload":    `{"k":"v"}`,
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("expected 2xx, got %d (body=%s)", status, body)
	}
}

// TestValidateFormatsRejectsBadIPv6 picks one new format and feeds it
// garbage to confirm the regex catches it. Optional `string?` fields
// are nil-guarded by the validator so a missing field passes; sending
// an explicit bad value triggers the regex.
func TestValidateFormatsRejectsBadIPv6(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/formats", map[string]any{
		"ipv6": "not-an-ipv6",
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "ipv6") {
		t.Errorf("expected ipv6 in message, got %s", body)
	}
}

// TestValidateFormatsAllowsMissingOptional confirms the nil-guard: an
// empty body must NOT trip @format on optional fields.
func TestValidateFormatsAllowsMissingOptional(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/formats", map[string]any{})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("expected 2xx, got %d (body=%s)", status, body)
	}
}

// TestValidateGenericPageHappyPath sends valid PageItem values inside
// a generic Page<PageItem>; every item's Validate() should pass via
// the runtime type-assertion path.
func TestValidateGenericPageHappyPath(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/page", map[string]any{
		"page": map[string]any{
			"items": []map[string]any{
				{"name": "alpha"},
				{"name": "beta"},
			},
			"total": 2,
		},
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("expected 2xx, got %d (body=%s)", status, body)
	}
}

// TestValidateGenericPageRejectsInnerEmpty proves the runtime
// type-assertion ACTUALLY invokes the inner item's Validate(): one
// PageItem has an empty name (violates @required), so the request
// must 400 with the inner field's error message.
func TestValidateGenericPageRejectsInnerEmpty(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/page", map[string]any{
		"page": map[string]any{
			"items": []map[string]any{
				{"name": "ok"},
				{"name": ""},
			},
			"total": 2,
		},
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "name: required") {
		t.Errorf("expected inner @required to fire, got %s", body)
	}
}

// TestValidateEnumHappyPath sends valid Priority + Tier + tags. The
// auto enum-value switch-case must accept every named constant.
func TestValidateEnumHappyPath(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/enum", map[string]any{
		"priority": "High",
		"tier":     2,
		"tags":     []string{"Low", "Normal", "Critical"},
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("expected 2xx, got %d (body=%s)", status, body)
	}
}

// TestValidateEnumRejectsUnknownValue feeds a string outside the
// declared Priority set; the auto-generated switch must reject.
func TestValidateEnumRejectsUnknownValue(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/enum", map[string]any{
		"priority": "Bogus",
		"tier":     2,
		"tags":     []string{"Low"},
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "invalid Priority value") {
		t.Errorf("expected enum-value error, got %s", body)
	}
}

// TestValidateEnumRequiredFiresFirst confirms decorator ordering:
// @required runs before the auto enum-value check, so an empty string
// triggers "required" rather than "invalid value".
func TestValidateEnumRequiredFiresFirst(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/enum", map[string]any{
		"priority": "",
		"tier":     2,
		"tags":     []string{"Low"},
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "priority: required") {
		t.Errorf("expected 'priority: required', got %s", body)
	}
}

// TestValidateEnumIntRequiredOnZero proves the int-valued enum's
// @required compares against the zero literal `0`, not "".
func TestValidateEnumIntRequiredOnZero(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/enum", map[string]any{
		"priority": "High",
		"tier":     0,
		"tags":     []string{"Low"},
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "tier: required") {
		t.Errorf("expected 'tier: required', got %s", body)
	}
}

// TestValidateEnumArrayRejectsBadElement covers Priority[]: every
// element must be in the enum set; one bad element fails the loop.
func TestValidateEnumArrayRejectsBadElement(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/enum", map[string]any{
		"priority": "High",
		"tier":     2,
		"tags":     []string{"Low", "NotAValue"},
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "tags: invalid Priority value") {
		t.Errorf("expected tags enum error, got %s", body)
	}
}

// TestValidateEnumOptionalAcceptsMissing pins the nil-guard on the
// optional `fallback` field — if it's not in the body the validator
// must skip the switch entirely.
func TestValidateEnumOptionalAcceptsMissing(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/validate/enum", map[string]any{
		"priority": "High",
		"tier":     2,
		"tags":     []string{"Low"},
		// fallback omitted
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Errorf("expected 2xx, got %d (body=%s)", status, body)
	}
}

// TestValidateGenericPageRejectsLength tests the OTHER inner validator
// (@length on PageItem.name) — proves the type-assertion path runs the
// full inner Validate(), not just one decorator.
func TestValidateGenericPageRejectsLength(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	tooLong := strings.Repeat("x", 80) // @length(1, 50) on PageItem.name
	status, body := postJSON(t, ts, "/api/v1/validate/page", map[string]any{
		"page": map[string]any{
			"items": []map[string]any{{"name": tooLong}},
			"total": 1,
		},
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "length out of range") {
		t.Errorf("expected length message, got %s", body)
	}
}
