package matrix

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// A required `file` a generic mixin supplies is refused when its part is
// missing, and reaches the service when sent.
func TestGenericMixinFileIsRequired(t *testing.T) {
	ts := bootAll(t)
	post := func(withDoc bool) (int, string) {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		form.WriteField("title", "t")
		if withDoc {
			part, err := form.CreateFormFile("doc", "report.pdf")
			if err != nil {
				t.Fatal(err)
			}
			part.Write([]byte("%PDF"))
		}
		form.Close()
		resp, err := http.Post(ts.URL+"/api/bindings/attach", form.FormDataContentType(), &body)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	if code, body := post(false); code != http.StatusBadRequest || !strings.Contains(body, "doc: required") {
		t.Errorf("without doc: got %d %s, want 400 doc: required", code, body)
	}
	code, body := post(true)
	var item struct{ Name string }
	if err := json.Unmarshal([]byte(body), &item); code != http.StatusCreated || err != nil || item.Name != "report.pdf" {
		t.Errorf("with doc: got %d %s, want 201 naming report.pdf", code, body)
	}
}
