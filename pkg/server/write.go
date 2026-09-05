package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// ErrNoDecoder is returned by WritePrecompressed when the client does not
// accept the stored content-coding and no decode function was supplied,
// so the body cannot be served in either form. Nothing has been written
// when it is returned.
var ErrNoDecoder = errors.New("server: client does not accept the stored content-coding and no decoder was supplied")

// WriteBytes writes a complete response in one go: contentType (when
// non-empty) and Content-Length are set, the status is written, then
// body. It is the building block for raw-response handlers that already
// hold the exact bytes they want on the wire and do not want the JSON
// encoder in the way.
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

// WritePrecompressed serves a body that is stored already compressed - a
// cache entry, a pre-built asset - without touching it when it can. When
// the client's Accept-Encoding lists coding (`"zstd"`, `"gzip"`, `"br"`,
// ...), the bytes go out verbatim with Content-Encoding set; otherwise
// decode turns them back into the identity form first. Either way the
// response carries `Vary: Accept-Encoding` so caches keep the two shapes
// apart. The framework pulls in no compression library: the caller, who
// already has one to fill the cache, supplies decode. A nil decode with
// a client that does not accept coding returns ErrNoDecoder before
// anything is written.
//
// The Compress middleware leaves a response that already carries
// Content-Encoding untouched, so the verbatim body is never re-encoded;
// on the decoded path Compress may gzip the identity bytes as usual.
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

// headerListsValue reports whether the comma-separated list header key
// already names value (case-insensitive), across every occurrence of the
// header. Keeps WritePrecompressed from stacking a second
// `Vary: Accept-Encoding` on top of the one Compress adds.
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
