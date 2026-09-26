package matrix

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/craftgodotdev/craftgo/pkg/server"
)

// A multipart handler answers a body it cannot parse as a failed validation,
// 400 with the parser's reason, and a body read past its cap 413, both as JSON
// {"message": ...}.
func TestMultipartParseFailures(t *testing.T) {
	ts := bootAll(t)
	capped := bootAll(t, func(srv *server.Server) { srv.SetDefaultMaxBodySize(64) })

	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	part, err := form.CreateFormFile("avatar_file", "a.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), 1024)); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	const avatar = "/api/bindings/users/u1/avatar"
	for _, tc := range []struct {
		name, url, contentType string
		body                   io.Reader
		status                 int
		want                   string
	}{
		{
			name: "not multipart", url: ts.URL + avatar, contentType: "application/json",
			body: strings.NewReader(`{}`), status: http.StatusBadRequest,
			want: "request Content-Type isn't multipart/form-data",
		},
		{
			name: "no boundary", url: ts.URL + avatar, contentType: "multipart/form-data",
			body: strings.NewReader("x"), status: http.StatusBadRequest,
			want: "no multipart boundary param in Content-Type",
		},
		{
			// The body goes without a length, so the parser, not the length check, meets the cap.
			name: "read past the cap", url: capped.URL + avatar, contentType: form.FormDataContentType(),
			body: io.NopCloser(bytes.NewReader(upload.Bytes())), status: http.StatusRequestEntityTooLarge,
			want: "request entity too large",
		},
	} {
		resp, err := http.Post(tc.url, tc.contentType, tc.body)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var got struct{ Message string }
		if err := json.Unmarshal(raw, &got); err != nil || resp.StatusCode != tc.status || got.Message != tc.want {
			t.Errorf("%s: got %d %q, want %d {\"message\":%q}", tc.name, resp.StatusCode, raw, tc.status, tc.want)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("%s: Content-Type %q, want JSON", tc.name, ct)
		}
	}
}
