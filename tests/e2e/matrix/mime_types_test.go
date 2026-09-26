package matrix

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
)

// @mimeTypes matches an upload's media type, its parameters and case aside, and a
// `type/*` range admits every subtype.
func TestMimeTypesMatchTheMediaType(t *testing.T) {
	ts := bootAll(t)
	for _, tc := range []struct {
		name        string
		parts       map[string]string // form name -> part Content-Type
		wantAllowed bool
	}{
		{"exact", map[string]string{"avatar_file": "image/png"}, true},
		{"case and parameters", map[string]string{"avatar_file": "IMAGE/PNG; name=a"}, true},
		{"range", map[string]string{"avatar_file": "image/jpeg", "thumb": "image/gif"}, true},
		{"outside the list", map[string]string{"avatar_file": "text/plain"}, false},
		{"outside the range", map[string]string{"avatar_file": "image/png", "thumb": "text/plain"}, false},
	} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for name, ct := range tc.parts {
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename="f"`, name))
			h.Set("Content-Type", ct)
			part, err := form.CreatePart(h)
			if err != nil {
				t.Fatal(err)
			}
			part.Write([]byte("x"))
		}
		form.Close()
		resp, err := http.Post(ts.URL+"/api/bindings/users/u1/avatar", form.FormDataContentType(), &body)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		refused := resp.StatusCode == http.StatusBadRequest && strings.Contains(string(raw), "disallowed content type")
		if tc.wantAllowed == refused {
			t.Errorf("%s: got %d %s, want allowed=%v", tc.name, resp.StatusCode, raw, tc.wantAllowed)
		}
	}
}
