package matrix

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	rawmodes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/service/raw_modes_service"
)

// rawDo issues one request against ts with an identity-only client (Go's
// default transport would otherwise add Accept-Encoding: gzip and
// transparently undo the encoding the tests want to observe).
func rawDo(t *testing.T, ts *httptest.Server, method, path string, headers map[string]string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestRawModes_PassthroughBare(t *testing.T) {
	ts := bootAll(t)
	resp, body := rawDo(t, ts, http.MethodGet, "/api/raw/pt", nil, nil)
	if resp.StatusCode != http.StatusOK || string(body) != "plain" {
		t.Errorf("got %d %q, want 200 plain", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("logic owns Content-Type; got %q", ct)
	}
}

// A passthrough with request/response blocks stays fully raw at runtime: the
// stub decodes the contract itself (and may reuse the generated Validate()).
func TestRawModes_PassthroughBlocksAreDocsOnly(t *testing.T) {
	ts := bootAll(t)
	resp, body := rawDo(t, ts, http.MethodGet, "/api/raw/pt/items/42", nil, nil)
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != `{"echo":"42"}` {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
	// The contract's @length(1, 32) is enforced by the stub, not the transport.
	resp, _ = rawDo(t, ts, http.MethodGet, "/api/raw/pt/items/"+strings.Repeat("x", 40), nil, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("stub-side Validate must yield 400, got %d", resp.StatusCode)
	}
}

func TestRawModes_PassthroughSeesTimeoutDeadline(t *testing.T) {
	ts := bootAll(t)
	_, body := rawDo(t, ts, http.MethodGet, "/api/raw/pt/timeout", nil, nil)
	if string(body) != "deadline=true" {
		t.Errorf("@timeout must put a deadline on a passthrough route; got %q", body)
	}
}

// The headline case: a body stored already compressed goes out verbatim
// when the client accepts the coding, and decoded otherwise.
func TestRawModes_RawResponsePrecompressedNegotiation(t *testing.T) {
	ts := bootAll(t)

	resp, body := rawDo(t, ts, http.MethodGet, "/api/raw/rr", map[string]string{"Accept-Encoding": "gzip"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", resp.Header.Get("Content-Encoding"))
	}
	if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary must name Accept-Encoding, got %q", resp.Header.Get("Vary"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
	plain, _ := io.ReadAll(zr)
	if string(plain) != rawmodes.CachedPlainJSON {
		t.Errorf("gunzipped body = %q", plain)
	}

	resp, body = rawDo(t, ts, http.MethodGet, "/api/raw/rr", nil, nil)
	if resp.Header.Get("Content-Encoding") != "" {
		t.Errorf("identity client must not get Content-Encoding, got %q", resp.Header.Get("Content-Encoding"))
	}
	if string(body) != rawmodes.CachedPlainJSON {
		t.Errorf("identity body = %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestRawModes_RawResponseValidatesBeforeLogic(t *testing.T) {
	ts := bootAll(t)
	json1 := map[string]string{"Content-Type": "application/json"}

	resp, _ := rawDo(t, ts, http.MethodPost, "/api/raw/rr/items/7", json1, strings.NewReader(`{"name":"x"}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("@minLength(2) must fail before logic runs: got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Handled") != "" {
		t.Errorf("logic must not run on a validation failure")
	}

	resp, body := rawDo(t, ts, http.MethodPost, "/api/raw/rr/items/7", json1, strings.NewReader(`{"name":"ok"}`))
	// @status(201) is docs-only on a raw response: the stub wrote 202.
	if resp.StatusCode != http.StatusAccepted || string(body) != "accepted" {
		t.Errorf("got %d %q, want 202 accepted", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Handled") != "7:ok" {
		t.Errorf("bound request must reach logic: X-Handled = %q", resp.Header.Get("X-Handled"))
	}
	if resp.Header.Get("Location") != "/api/raw/rr/items/7" {
		t.Errorf("Location is logic's to write: got %q", resp.Header.Get("Location"))
	}
}

func multipartBody(t *testing.T, fields map[string]string, fileField, fileName string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if fileField != "" {
		fw, err := mw.CreateFormFile(fileField, fileName)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(content)
	}
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestRawModes_RawResponseOverMultipart(t *testing.T) {
	ts := bootAll(t)
	buf, ct := multipartBody(t, map[string]string{"note": "hi"}, "doc", "a.bin", bytes.Repeat([]byte{7}, 123))
	resp, body := rawDo(t, ts, http.MethodPost, "/api/raw/rr/upload", map[string]string{"Content-Type": ct}, buf)
	if resp.StatusCode != http.StatusOK || string(body) != "note=hi size=123" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
	buf, ct = multipartBody(t, map[string]string{"note": ""}, "doc", "a.bin", []byte{1})
	resp, _ = rawDo(t, ts, http.MethodPost, "/api/raw/rr/upload", map[string]string{"Content-Type": ct}, buf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("form validation must run before the raw response: got %d", resp.StatusCode)
	}
}

func TestRawModes_RawResponseCrossPackageRequest(t *testing.T) {
	ts := bootAll(t)
	resp, body := rawDo(t, ts, http.MethodGet, "/api/raw/rr/xpkg/abc?sev=Warning", nil, nil)
	if resp.StatusCode != http.StatusOK || string(body) != "abc:Warning" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
	resp, _ = rawDo(t, ts, http.MethodGet, "/api/raw/rr/xpkg/abc?sev=Bogus", nil, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("cross-package enum validation must run: got %d", resp.StatusCode)
	}
}

// A raw handler that committed part of a body and then returned an error
// keeps its committed response: no envelope is spliced into the body.
func TestRawModes_RawResponseLateErrorKeepsCommittedBody(t *testing.T) {
	ts := bootAll(t)
	resp, body := rawDo(t, ts, http.MethodGet, "/api/raw/rr/late", nil, nil)
	if resp.StatusCode != http.StatusOK || string(body) != "partial-" {
		t.Errorf("got %d %q, want 200 partial-", resp.StatusCode, body)
	}
}

func TestRawModes_RawResponseIgnoreMiddleware(t *testing.T) {
	ts := bootAll(t)
	resp, body := rawDo(t, ts, http.MethodGet, "/api/raw/rr/nomw", nil, nil)
	if resp.StatusCode != http.StatusOK || string(body) != "nomw" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
}

func TestRawModes_RawRequestNoResponseIs204(t *testing.T) {
	ts := bootAll(t)
	resp, body := rawDo(t, ts, http.MethodGet, "/api/raw/rq/items/5", nil, nil)
	if resp.StatusCode != http.StatusNoContent || len(body) != 0 {
		t.Errorf("got %d %q, want 204 empty", resp.StatusCode, body)
	}
}

func TestRawModes_RawRequestBodyIsNotDecoded(t *testing.T) {
	ts := bootAll(t)
	garbage := "definitely not json"
	resp, body := rawDo(t, ts, http.MethodPost, "/api/raw/rq", map[string]string{"Content-Type": "application/json"}, strings.NewReader(garbage))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("typed response after a raw request keeps the verb default 201, got %d %q", resp.StatusCode, body)
	}
	var out struct {
		Accepted int `json:"accepted"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("response must be JSON: %v (%q)", err, body)
	}
	if out.Accepted != len(garbage) {
		t.Errorf("stub must see the raw body: accepted = %d, want %d", out.Accepted, len(garbage))
	}
}

func TestRawModes_RawRequestGenericResponse(t *testing.T) {
	ts := bootAll(t)
	resp, body := rawDo(t, ts, http.MethodGet, "/api/raw/rq/page", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 || page.Items[1].ID != "b" {
		t.Errorf("unexpected page %s", body)
	}
}

func TestRawModes_RawRequestMultipartContractIsDocsOnly(t *testing.T) {
	ts := bootAll(t)
	buf, ct := multipartBody(t, map[string]string{"note": "n"}, "doc", "a.bin", []byte("payload"))
	size := buf.Len()
	resp, body := rawDo(t, ts, http.MethodPost, "/api/raw/rq/docs", map[string]string{"Content-Type": ct}, buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	var out struct {
		Accepted int `json:"accepted"`
	}
	_ = json.Unmarshal(body, &out)
	if out.Accepted != size {
		t.Errorf("transport must not consume the multipart body: accepted = %d, want %d", out.Accepted, size)
	}
}

func TestRawModes_RawRequestLimitsApply(t *testing.T) {
	ts := bootAll(t)
	resp, _ := rawDo(t, ts, http.MethodPost, "/api/raw/rq/limits", nil, bytes.NewReader(bytes.Repeat([]byte("x"), 2048)))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("@maxBodySize(1KB) pre-check must reject 2KB: got %d", resp.StatusCode)
	}
	resp, _ = rawDo(t, ts, http.MethodPost, "/api/raw/rq/limits", nil, strings.NewReader("small"))
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("small body under @timeout must succeed with a deadline present: got %d", resp.StatusCode)
	}
}

func TestRawModes_ExtendHeaderPropagatesRawResponse(t *testing.T) {
	ts := bootAll(t)
	resp, body := rawDo(t, ts, http.MethodGet, "/api/raw/ext/raw", nil, nil)
	if resp.StatusCode != http.StatusOK || string(body) != "ext-raw" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
	resp, body = rawDo(t, ts, http.MethodGet, "/api/raw/ext/typed/9", nil, nil)
	if resp.StatusCode != http.StatusOK || string(body) != "ext:9" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
	resp, _ = rawDo(t, ts, http.MethodGet, "/api/raw/ext/typed/"+strings.Repeat("x", 40), nil, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bound request on the propagated raw response must still validate: got %d", resp.StatusCode)
	}
}
