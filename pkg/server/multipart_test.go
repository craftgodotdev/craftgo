package server

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
)

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// formBody builds a multipart body with a text part and a file part per entry of files.
func formBody(t *testing.T, files map[string]string) (body []byte, contentType string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("note", "hi"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"small", "big", "free"} {
		content, ok := files[name]
		if !ok {
			continue
		}
		fw, err := mw.CreateFormFile(name, name+".bin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(fw, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), mw.FormDataContentType()
}

// parseLimited parses body as r.ParseMultipartForm does after LimitParts, its reads split by
// split, and returns the request, how much of the body was read and the parse error.
func parseLimited(body []byte, contentType string, limits map[string]int64, split func(io.Reader) io.Reader) (*http.Request, int, error) {
	cr := &countingReader{r: split(bytes.NewReader(body))}
	r := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(cr))
	r.Header.Set("Content-Type", contentType)
	LimitParts(r, limits)
	err := r.ParseMultipartForm(MultipartMemory)
	return r, cr.n, err
}

// A part under its limit parses whole, a part over it fails the read at the limit with the
// @maxSize message, and a part without a limit takes any size; in whole reads, byte by byte and
// in a body whose lines end in a bare LF.
func TestLimitParts(t *testing.T) {
	limits := map[string]int64{"small": 8, "big": 1024}
	for name, split := range map[string]func(io.Reader) io.Reader{
		"whole":       func(r io.Reader) io.Reader { return r },
		"byte a read": iotest.OneByteReader,
	} {
		for _, lf := range []bool{false, true} {
			prep := func(b []byte) []byte {
				if lf {
					return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
				}
				return b
			}
			body, ct := formBody(t, map[string]string{"small": "12345678", "big": strings.Repeat("y", 1024), "free": strings.Repeat("z", 1<<20)})
			r, _, err := parseLimited(prep(body), ct, limits, split)
			if err != nil {
				t.Fatalf("%s, lf %v: parts within their limits: %v", name, lf, err)
			}
			if fh := r.MultipartForm.File["big"]; len(fh) != 1 || fh[0].Size != 1024 || r.MultipartForm.File["free"][0].Size != 1<<20 || r.PostForm.Get("note") != "hi" {
				t.Fatalf("%s, lf %v: the form lost a part: %+v", name, lf, r.MultipartForm)
			}

			body, ct = formBody(t, map[string]string{"small": "123456789", "big": strings.Repeat("y", 1<<20)})
			_, read, err := parseLimited(prep(body), ct, limits, split)
			var tooLarge *partTooLarge
			if !errors.As(err, &tooLarge) || tooLarge.Error() != "small: file size exceeds 8 bytes" {
				t.Errorf("%s, lf %v: a part past its limit: got %v", name, lf, err)
			}
			if read > 64<<10 {
				t.Errorf("%s, lf %v: read %d bytes of a %d-byte body before refusing", name, lf, read, len(body))
			}
		}
	}
}

// Content holding the boundary but not as a delimiter, which the parser keeps as content, does not
// throw the count off.
func TestLimitPartsBoundaryInContent(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	content := "\n--" + mw.Boundary() + "x" + strings.Repeat("y", 100)
	fw, _ := mw.CreateFormFile("small", "a.bin")
	_, _ = io.WriteString(fw, content)
	_ = mw.Close()
	_, _, err := parseLimited(buf.Bytes(), mw.FormDataContentType(), map[string]int64{"small": int64(len(content))}, iotest.OneByteReader)
	if err != nil {
		t.Errorf("a part at its limit: %v", err)
	}
	_, _, err = parseLimited(buf.Bytes(), mw.FormDataContentType(), map[string]int64{"small": int64(len(content)) - 10}, iotest.OneByteReader)
	if err == nil {
		t.Error("a part past its limit passed")
	}
}

// A body that is not a multipart form is left alone.
func TestLimitPartsLeavesOtherBodies(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	body := r.Body
	LimitParts(r, map[string]int64{"x": 1})
	if r.Body != body {
		t.Error("a JSON body was wrapped")
	}
}

// A part past its limit is answered 400 with the @maxSize message, a body past the route's cap
// still 413.
func TestLimitPartsAnswers(t *testing.T) {
	body, ct := formBody(t, map[string]string{"small": "123456789"})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set("Content-Type", ct)
	LimitParts(r, map[string]int64{"small": 8})
	WriteValidationError(rec, r, r.ParseMultipartForm(MultipartMemory))
	if rec.Code != http.StatusBadRequest || rec.Body.String() != "{\"message\":\"small: file size exceeds 8 bytes\"}\n" {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}

	var cap413 int
	h := maxBodySizeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		LimitParts(r, map[string]int64{"small": 1 << 20})
		WriteValidationError(w, r, r.ParseMultipartForm(MultipartMemory))
	}), 16)
	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/", io.NopCloser(bytes.NewReader(body)))
	r.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, r)
	cap413 = rec.Code
	if cap413 != http.StatusRequestEntityTooLarge {
		t.Errorf("past the route's cap: got %d, want 413", cap413)
	}
}
