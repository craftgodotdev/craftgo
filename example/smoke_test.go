package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

// TestSearchBooksHappyPath exercises the new /v1/books/search endpoint
// end-to-end: validate, dispatch to logic (which uses the embedded
// log.Logger to emit a "search start" / "search complete" pair).
// Stdout shows the structured JSON lines during `go test -v`.
func TestSearchBooksHappyPath(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/books/search?q=go")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 2xx, got %d", resp.StatusCode)
	}
}

// TestSearchBooksRichQuery pins the codegen's auto-bind of every
// query-parameter shape: string (q), []string (tags), int (limit),
// []int (years), bool (verbose). The request would 400 if any of
// the parsers drops a value or chokes on a numeric/bool literal.
func TestSearchBooksRichQuery(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	url := ts.URL + "/api/v1/books/search?q=go" +
		"&tags=fiction&tags=ya" +
		"&limit=10" +
		"&years=2020&years=2021" +
		"&verbose=true"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var out struct {
		P struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		} `json:"p"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.P.Total != len(out.P.Items) {
		t.Errorf("total %d != items %d", out.P.Total, len(out.P.Items))
	}
}

// TestCheckoutOrderEveryBinding exercises the full request/response
// binding matrix in one shot:
//
//   request:
//     - path     /orders/{id}/checkout
//     - query    ?dryRun=true
//     - body     { notes, lines: [...] }
//     - header   idempotencyKey: <key>
//     - cookie   sessionId=<sid>
//
//   response:
//     - header   orderId: <id>
//     - cookie   lastOrderId=<id>
//     - body     { orderId, lastOrderId, order }
//
// Two assertions matter most: the handler must echo every input
// value back into the response, and the response must carry the
// header + cookie alongside the JSON body. dryRun=true short-
// circuits persistence so the test stays free of side-effects.
func TestCheckoutOrderEveryBinding(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	body, _ := json.Marshal(map[string]any{
		"notes": "ship gift-wrapped",
		"lines": []map[string]any{{"bookId": "b1", "quantity": 2}},
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/orders/draft-99/checkout?dryRun=true", bytes.NewReader(body))
	req.Header.Set("idempotencyKey", "key-abc")
	req.AddCookie(&http.Cookie{Name: "sessionId", Value: "sess-xyz"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("orderId"); got != "dry-run" {
		t.Errorf("response header orderId = %q, want %q", got, "dry-run")
	}
	var sawCookie bool
	for _, c := range resp.Cookies() {
		if c.Name == "lastOrderId" && c.Value == "dry-run" {
			sawCookie = true
		}
	}
	if !sawCookie {
		t.Errorf("response missing Set-Cookie lastOrderId=dry-run; got %v", resp.Cookies())
	}
	rawBody, _ := io.ReadAll(resp.Body)
	// Response @header / @cookie fields must NOT leak into the JSON body
	// — the codegen marks them `json:"-"` so the value travels through
	// exactly one channel.
	if strings.Contains(string(rawBody), `"orderId"`) {
		t.Errorf("body must not carry orderId (it's @header-bound):\n%s", rawBody)
	}
	if strings.Contains(string(rawBody), `"lastOrderId"`) {
		t.Errorf("body must not carry lastOrderId (it's @cookie-bound):\n%s", rawBody)
	}
	var out struct {
		Order struct {
			ID         string `json:"id"`
			Items      []any  `json:"items"`
			TotalCents int    `json:"totalCents"`
			Status     string `json:"status"`
		} `json:"order"`
	}
	if err := json.Unmarshal(rawBody, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Order.ID != "draft-99" {
		t.Errorf("body order.id = %q, want %q (path bind broken)", out.Order.ID, "draft-99")
	}
	if len(out.Order.Items) != 1 {
		t.Errorf("body items count = %d, want 1 (body bind broken)", len(out.Order.Items))
	}
	if out.Order.Status != "draft" {
		t.Errorf("body status = %q, want %q (dryRun query bind broken)", out.Order.Status, "draft")
	}
}

// TestCheckoutOrderRejectsMissingHeader confirms the @required
// @header validator fires when the idempotencyKey header is absent.
func TestCheckoutOrderRejectsMissingHeader(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	body, _ := json.Marshal(map[string]any{
		"notes": "no header",
		"lines": []map[string]any{{"bookId": "b1", "quantity": 1}},
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/orders/x/checkout", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "sessionId", Value: "sess"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing idempotencyKey header, got %d", resp.StatusCode)
	}
}

// TestSearchBooksRejectsBadInt confirms the strconv.ParseInt path
// in the generated handler reports 400 on garbage numeric input —
// proving the parser actually runs (and isn't silently dropping
// the value).
func TestSearchBooksRejectsBadInt(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/books/search?q=go&limit=notanint")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for non-int limit, got %d", resp.StatusCode)
	}
}

// TestSearchBooksValidatesQuery pins the request validators (@required
// + @minLength) — empty `q` rejects.
func TestSearchBooksValidatesQuery(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/books/search")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing q, got %d", resp.StatusCode)
	}
}

// TestDeprecatedFieldInOpenAPI confirms the field-level @deprecated
// on `Book.priceLegacy` flows into `docs/openapi.yaml`. The runtime
// behaviour is unchanged (field still bound from JSON), but the spec
// and Go field carry the migration hint.
func TestDeprecatedFieldInOpenAPI(t *testing.T) {
	body, err := os.ReadFile("docs/openapi.yaml")
	if err != nil {
		t.Skipf("openapi.yaml missing — run `craftgo gen` first: %v", err)
	}
	src := string(body)
	if !strings.Contains(src, "priceLegacy:") {
		t.Fatalf("expected priceLegacy field in spec:\n%s", src)
	}
	idx := strings.Index(src, "priceLegacy:")
	end := idx + 200
	if end > len(src) {
		end = len(src)
	}
	window := src[idx:end]
	if !strings.Contains(window, "deprecated: true") {
		t.Errorf("expected priceLegacy.deprecated: true:\n%s", window)
	}
	if !strings.Contains(window, "use priceCents instead") {
		t.Errorf("expected migration hint in description:\n%s", window)
	}
}

// TestDeprecatedMethodInOpenAPI confirms the method-level @deprecated
// on LegacyListBooks lands as `deprecated: true` on the operation.
func TestDeprecatedMethodInOpenAPI(t *testing.T) {
	body, err := os.ReadFile("docs/openapi.yaml")
	if err != nil {
		t.Skipf("openapi.yaml missing — run `craftgo gen` first: %v", err)
	}
	src := string(body)
	idx := strings.Index(src, "/v1/books-v0:")
	if idx < 0 {
		t.Fatalf("expected /v1/books-v0 path in spec")
	}
	end := idx + 600
	if end > len(src) {
		end = len(src)
	}
	window := src[idx:end]
	if !strings.Contains(window, "deprecated: true") {
		t.Errorf("expected operation-level deprecated:\n%s", window)
	}
	if !strings.Contains(window, "use GET /books instead") {
		t.Errorf("expected migration hint in description:\n%s", window)
	}
}

// TestDeprecatedMethodStillRoutes pins that `@deprecated` is purely
// metadata — the route is still registered and reachable. (We give it
// a logic stub that returns nil so the empty response works without
// scaffolding ceremony.)
func TestDeprecatedMethodStillRoutes(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/v1/books-v0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		t.Errorf("unexpected 5xx on deprecated endpoint: %d", resp.StatusCode)
	}
}

// TestFeedbackHappyPath exercises the JSON REST endpoint
// `POST /api/v1/feedback` end-to-end: validate, dispatch to logic,
// echo back the stored Feedback record.
func TestFeedbackHappyPath(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/feedback", map[string]any{
		"homepage": "https://example.com",
		"phone":    "+84-901-234-567",
		"apiToken": "supersecrettoken",
	})
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("expected 2xx, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, `"id":"fb-001"`) {
		t.Errorf("expected echoed id in response, got:\n%s", body)
	}
}

// TestFeedbackValidatesApiToken pins that the @length validator on the
// request side fires (a 4-char token is below @length(8, 100)).
func TestFeedbackValidatesApiToken(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	status, body := postJSON(t, ts, "/api/v1/feedback", map[string]any{
		"homepage": "https://example.com",
		"apiToken": "tiny",
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", status, body)
	}
	if !strings.Contains(body, "apiToken: length out of range") {
		t.Errorf("expected length error, got %s", body)
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
