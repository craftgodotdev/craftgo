package server

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rawRequest sends reqLine to addr over its own connection and reads the response.
func rawRequest(t *testing.T, addr, reqLine string) (*http.Response, string) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := fmt.Fprintf(c, "%s\r\nHost: x\r\nConnection: close\r\n\r\n", reqLine); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

// Every error the framework writes is {"message": ...} as JSON with nosniff, the message being
// the lower-case status text, or the error's text for a validation error; a redirect and the
// asterisk-form 400 stay the mux's own.
func TestFrameworkErrorsAreJSON(t *testing.T) {
	observeLogs(t)
	srv := New(nil)
	srv.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
	srv.HandleFunc("GET /invalid", func(w http.ResponseWriter, r *http.Request) {
		WriteValidationError(w, r, errors.New("name: minLength 3"))
	})
	srv.HandleFunc("GET /unknown", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, errors.New("db down"))
	})
	srv.Handle("POST /capped", WithLimits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v struct{ A string }
		if err := JSON().Decode(r.Body, &v); err != nil {
			WriteValidationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), Limits{MaxBodySize: 8}))
	srv.HandleFunc("GET /items/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	msg := func(m string) string { return `{"message":"` + m + `"}` + "\n" }
	const overCap = `{"A":"0123456789"}`
	for _, tc := range []struct {
		name, method, path string
		body               io.Reader
		status             int
		wantBody           string
		header, want       string
	}{
		{"panic", "GET", "/panic", nil, 500, msg("internal server error"), "", ""},
		{"validation", "GET", "/invalid", nil, 400, msg("name: minLength 3"), "", ""},
		{"unknown error", "GET", "/unknown", nil, 500, msg("internal server error"), "", ""},
		{"declared length over the cap", "POST", "/capped", strings.NewReader(overCap), 413, msg("request entity too large"), "", ""},
		{"read past the cap", "POST", "/capped", io.NopCloser(strings.NewReader(overCap)), 413, msg("request entity too large"), "", ""},
		{"no route", "GET", "/nope", nil, 404, msg("not found"), "", ""},
		{"HEAD, no route", "HEAD", "/nope", nil, 404, "", "", ""},
		{"wrong method", "DELETE", "/panic", nil, 405, msg("method not allowed"), "Allow", "GET, HEAD"},
	} {
		req, err := http.NewRequest(tc.method, ts.URL+tc.path, tc.body)
		if err != nil {
			t.Fatal(err)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != tc.status || string(body) != tc.wantBody {
			t.Errorf("%s: got %d %q, want %d %q", tc.name, res.StatusCode, body, tc.status, tc.wantBody)
		}
		if ct := res.Header.Get("Content-Type"); ct != contentTypeJSON {
			t.Errorf("%s: Content-Type %q, want JSON", tc.name, ct)
		}
		if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options %q, want nosniff", tc.name, got)
		}
		if tc.header != "" && res.Header.Get(tc.header) != tc.want {
			t.Errorf("%s: %s %q, want %q", tc.name, tc.header, res.Header.Get(tc.header), tc.want)
		}
	}

	addr := ts.Listener.Addr().String()
	if res, body := rawRequest(t, addr, "CONNECT example.com:443 HTTP/1.1"); res.StatusCode != 404 || body != msg("not found") {
		t.Errorf("CONNECT: got %d %q, want the JSON 404", res.StatusCode, body)
	}
	if res, body := rawRequest(t, addr, "GET * HTTP/1.1"); res.StatusCode != 400 || body != "" {
		t.Errorf("GET *: got %d %q, want the mux's empty 400", res.StatusCode, body)
	}
	for path, location := range map[string]string{"/items": "/items/", "/a/../nope": "/nope"} {
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusTemporaryRedirect || res.Header.Get("Location") != location {
			t.Errorf("GET %s: got %d to %q, want a 307 to %s", path, res.StatusCode, res.Header.Get("Location"), location)
		}
	}
}

// A read past a body cap is answered 413 without the validation hook, wrapped or not, and the
// multipart limit counts as one; a committed response is left to the hook.
func TestWriteValidationErrorAnswersBodyTooLarge413(t *testing.T) {
	called := 0
	SetDefaultValidationFailed(func(w http.ResponseWriter, _ *http.Request, _ error) {
		called++
		w.WriteHeader(http.StatusTeapot)
	})
	t.Cleanup(func() { SetDefaultValidationFailed(nil) })
	for _, err := range []error{
		&http.MaxBytesError{Limit: 8},
		fmt.Errorf("multipart: NextPart: %w", &http.MaxBytesError{Limit: 8}),
		multipart.ErrMessageTooLarge,
	} {
		rec := httptest.NewRecorder()
		WriteValidationError(rec, httptest.NewRequest(http.MethodPost, "/x", nil), err)
		if rec.Code != http.StatusRequestEntityTooLarge || rec.Body.String() != `{"message":"request entity too large"}`+"\n" {
			t.Errorf("%v: got %d %q, want the JSON 413", err, rec.Code, rec.Body.String())
		}
	}
	if called != 0 {
		t.Errorf("the validation hook ran %d times for a body over the cap", called)
	}

	rec := httptest.NewRecorder()
	tw := &trackingWriter{ResponseWriter: rec}
	tw.WriteHeader(http.StatusOK)
	WriteValidationError(tw, httptest.NewRequest(http.MethodPost, "/x", nil), &http.MaxBytesError{Limit: 8})
	if called != 1 || rec.Code != http.StatusOK {
		t.Errorf("after commit: hook ran %d times, status %d; want the hook once and the 200 kept", called, rec.Code)
	}
}

// A multipart body cut by its cap answers 413 without the validation hook wherever the cap
// falls, a part header included, where the parser reports a malformed header.
func TestMultipartBodyCutByItsCapAnswers413(t *testing.T) {
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	_ = mw.WriteField("caption", "a holiday photo")
	part, _ := mw.CreateFormFile("file", "a.png")
	_, _ = part.Write(bytes.Repeat([]byte("x"), 64))
	_ = mw.WriteField("album", "summer")
	_ = mw.Close()
	body := form.Bytes()

	called := 0
	SetDefaultValidationFailed(func(w http.ResponseWriter, _ *http.Request, _ error) {
		called++
		w.WriteHeader(http.StatusTeapot)
	})
	t.Cleanup(func() { SetDefaultValidationFailed(nil) })
	upload := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			WriteValidationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	for limit := int64(1); limit < int64(len(body)); limit++ {
		req := httptest.NewRequest(http.MethodPost, "/upload", io.NopCloser(bytes.NewReader(body)))
		req.ContentLength = -1
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		WithLimits(upload, Limits{MaxBodySize: limit}).ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("cap %d of %d bytes: status %d %q, want 413", limit, len(body), rec.Code, rec.Body.String())
		}
	}
	if called != 0 {
		t.Errorf("the validation hook ran %d times for a body over its cap", called)
	}
}

// WriteValidationError with a nil request answers as it does with one, and once the response
// is committed leaves it as it is.
func TestWriteValidationErrorTakesANilRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteValidationError(rec, nil, errors.New("bad"))
	if rec.Code != http.StatusBadRequest || rec.Body.String() != `{"message":"bad"}`+"\n" {
		t.Errorf("got %d %q, want the JSON 400", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	tw := &trackingWriter{ResponseWriter: rec}
	tw.WriteHeader(http.StatusOK)
	WriteValidationError(tw, nil, errors.New("bad"))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("after commit: got %d %q, want the 200 kept", rec.Code, rec.Body.String())
	}
}

// An error response drops a Content-Length set for another body.
func TestErrorResponsesDropAStaleContentLength(t *testing.T) {
	for name, write := range map[string]func(http.ResponseWriter, *http.Request){
		"validation": func(w http.ResponseWriter, r *http.Request) { WriteValidationError(w, r, errors.New("bad")) },
		"status": func(w http.ResponseWriter, r *http.Request) {
			WriteError(w, r, fakeStatusError{msg: "gone", status: 410})
		},
		"unknown": func(w http.ResponseWriter, r *http.Request) { WriteError(w, r, errors.New("db down")) },
	} {
		observeLogs(t)
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Length", "1000")
		write(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
		if got := rec.Header().Get("Content-Length"); got != "" {
			t.Errorf("%s: Content-Length %q survived the error body", name, got)
		}
	}
}
