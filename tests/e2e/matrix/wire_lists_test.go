package matrix

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
)

// A list header binds every element of every X-Ids line, split at commas.
func TestListHeaderSplitsAtCommas(t *testing.T) {
	ts := bootAll(t)
	for _, tc := range []struct {
		name  string
		lines []string
		want  int
	}{
		{"one line", []string{"1,2"}, http.StatusOK},
		{"spaces", []string{"1, 2, 3"}, http.StatusOK},
		{"two lines", []string{"1", "2"}, http.StatusOK},
		{"too few", []string{"1"}, http.StatusBadRequest},
		{"bad element", []string{"1,x"}, http.StatusBadRequest},
	} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/bindings/ids", nil)
		for _, l := range tc.lines {
			req.Header.Add("X-Ids", l)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: got %d %s, want %d", tc.name, resp.StatusCode, raw, tc.want)
		}
	}
}

// A text part binds from the multipart body alone: a query value of the same name neither
// overrides the part nor stands in for a missing one.
func TestFormPartIgnoresTheQuery(t *testing.T) {
	ts := bootAll(t)
	tooLong := strings.Repeat("x", 281) // note is @maxLength(280)
	for _, tc := range []struct {
		name string
		note *string
	}{
		{"part beside a query value", ptr("ok")},
		{"query value without a part", nil},
	} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="avatar_file"; filename="a.png"`)
		h.Set("Content-Type", "image/png")
		part, _ := form.CreatePart(h)
		part.Write([]byte("x"))
		if tc.note != nil {
			form.WriteField("note", *tc.note)
		}
		form.Close()
		resp, err := http.Post(ts.URL+"/api/bindings/users/u1/avatar?note="+tooLong, form.FormDataContentType(), &body)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusBadRequest {
			t.Errorf("%s: got 400 %s; the query value must not bind the part", tc.name, raw)
		}
	}
}

func ptr[T any](v T) *T { return &v }
