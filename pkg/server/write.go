package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// ErrNoDecoder is returned by [WritePrecompressed], before anything is written, when the
// client does not accept the stored coding and decode is nil.
var ErrNoDecoder = errors.New("server: client does not accept the stored content-coding and no decoder was supplied")

// WriteBytes writes status and body with Content-Length set, and Content-Type set to
// contentType unless it is empty. It returns the body write's error.
func WriteBytes(w http.ResponseWriter, status int, contentType string, body []byte) error {
	h := w.Header()
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, err := w.Write(body)
	return err
}

// WritePrecompressed writes body, stored encoded with coding, as is with Content-Encoding
// when the client accepts coding, and as decode(body) otherwise; it adds Vary: Accept-Encoding.
// A nil decode ([ErrNoDecoder]) or decode's error is returned before anything is written.
func WritePrecompressed(w http.ResponseWriter, r *http.Request, status int, contentType, coding string, body []byte, decode func([]byte) ([]byte, error)) error {
	h := w.Header()
	if !headerListsValue(h, "Vary", "Accept-Encoding") {
		h.Add("Vary", "Accept-Encoding")
	}
	if AcceptsEncoding(r, coding) {
		h.Set("Content-Encoding", coding)
		return WriteBytes(w, status, contentType, body)
	}
	if decode == nil {
		return ErrNoDecoder
	}
	plain, err := decode(body)
	if err != nil {
		return err
	}
	return WriteBytes(w, status, contentType, plain)
}

// headerListsValue reports whether any key header lists value, case-insensitively.
func headerListsValue(h http.Header, key, value string) bool {
	for _, v := range h.Values(key) {
		for part := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return true
			}
		}
	}
	return false
}
