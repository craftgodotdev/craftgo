package server

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// CompressOptions tunes the response compression middleware.
type CompressOptions struct {
	// MinSize is the threshold below which responses skip compression.
	// Bodies smaller than this are released uncompressed because the
	// CPU cost outweighs the wire-size win. Defaults to 1024.
	MinSize int
	// Level is the gzip / deflate compression level (1..9, or
	// gzip.DefaultCompression). Defaults to gzip.DefaultCompression.
	Level int
	// SkipTypes overrides the default list of Content-Type prefixes
	// that bypass compression (media that's already byte-compressed
	// by its own format). Pass an empty slice to compress everything.
	SkipTypes []string
}

// defaultSkipTypes are Content-Type prefixes whose payloads are already
// compressed by the format itself; recompressing burns CPU for no gain.
var defaultSkipTypes = []string{
	"image/", "video/", "audio/",
	"application/zip", "application/gzip", "application/x-gzip",
	"application/x-bzip2", "application/x-7z-compressed",
	"application/x-rar-compressed", "application/x-tar",
	"font/woff", "font/woff2",
}

// Compress returns middleware that gzip- or deflate-compresses responses
// when the client advertises a matching Accept-Encoding. Small bodies
// (< MinSize) and pre-compressed media types pass through untouched.
//
// Vary: Accept-Encoding is added to every response so caches keep the
// negotiated and unnegotiated copies separate.
func Compress(opts ...CompressOptions) Middleware {
	o := CompressOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.MinSize == 0 {
		o.MinSize = 1024
	}
	if o.Level == 0 {
		o.Level = gzip.DefaultCompression
	}
	skip := defaultSkipTypes
	if o.SkipTypes != nil {
		skip = o.SkipTypes
	}

	gzipPool := &sync.Pool{New: func() any {
		gz, _ := gzip.NewWriterLevel(io.Discard, o.Level)
		return gz
	}}
	deflatePool := &sync.Pool{New: func() any {
		df, _ := flate.NewWriter(io.Discard, o.Level)
		return df
	}}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", "Accept-Encoding")
			enc := negotiateEncoding(r.Header.Get("Accept-Encoding"))
			if enc == "" || r.Method == http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}
			cw := &compressWriter{
				ResponseWriter: w,
				encoding:       enc,
				minSize:        o.MinSize,
				skipTypes:      skip,
				gzipPool:       gzipPool,
				deflatePool:    deflatePool,
			}
			defer cw.Close()
			next.ServeHTTP(cw, r)
		})
	}
}

// negotiateEncoding returns "gzip", "deflate", or "" based on what the
// client advertises. The first supported token with a non-zero quality wins; a
// token carrying `;q=0` is an explicit refusal of that coding (RFC 7231
// §5.3.1) and is skipped, so a client sending `gzip;q=0` receives an
// uncompressed response.
func negotiateEncoding(accept string) string {
	if accept == "" {
		return ""
	}
	for part := range strings.SplitSeq(accept, ",") {
		token := strings.TrimSpace(part)
		params := ""
		if i := strings.IndexByte(token, ';'); i >= 0 {
			params = token[i+1:]
			token = token[:i]
		}
		if qualityIsZero(params) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(token)) {
		case "gzip":
			return "gzip"
		case "deflate":
			return "deflate"
		}
	}
	return ""
}

// qualityIsZero reports whether an Accept-Encoding parameter list pins the
// quality to zero (`q=0`, `q=0.0`, ...) — an explicit "do not use this coding".
func qualityIsZero(params string) bool {
	for seg := range strings.SplitSeq(params, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(seg), "=")
		if !ok || strings.ToLower(strings.TrimSpace(k)) != "q" {
			continue
		}
		switch strings.TrimSpace(v) {
		case "0", "0.", "0.0", "0.00", "0.000":
			return true
		}
	}
	return false
}

// resettableWriter is the subset of gzip.Writer / flate.Writer the
// pool path needs.
type resettableWriter interface {
	io.WriteCloser
	Flush() error
	Reset(io.Writer)
}

// compressWriter buffers the head of the response so it can decide
// whether the payload is large enough and of a compressible
// Content-Type to be worth encoding. Once the decision is made the
// remaining writes go straight through the chosen sink.
type compressWriter struct {
	http.ResponseWriter
	encoding    string
	minSize     int
	skipTypes   []string
	gzipPool    *sync.Pool
	deflatePool *sync.Pool

	statusCode int
	headerSet  bool
	decided    bool
	compress   bool

	buf bytes.Buffer
	cmp resettableWriter
}

// WriteHeader records the status code without forwarding; the actual
// upstream WriteHeader call happens once the compress / passthrough
// decision is made.
func (cw *compressWriter) WriteHeader(code int) {
	if cw.headerSet {
		return
	}
	cw.statusCode = code
	cw.headerSet = true
}

// Unwrap exposes the wrapped writer so http.ResponseController (and net/http's
// hijack path) can reach the underlying Hijacker — a connection Hijack (e.g. a
// WebSocket upgrade) takes over the raw conn and bypasses compression, which is
// the correct behaviour. Without Unwrap the upgrade fails with "feature not
// supported" when Compress is in the chain.
func (cw *compressWriter) Unwrap() http.ResponseWriter { return cw.ResponseWriter }

// Write accumulates bytes until the threshold is crossed, then commits
// to compressed encoding. Once committed, every subsequent Write goes
// straight through the encoder.
func (cw *compressWriter) Write(b []byte) (int, error) {
	if !cw.headerSet {
		cw.WriteHeader(http.StatusOK)
	}
	if cw.decided {
		if cw.compress {
			return cw.cmp.Write(b)
		}
		return cw.ResponseWriter.Write(b)
	}
	if cw.shouldSkipImmediately() {
		cw.commitPassthrough()
		return cw.ResponseWriter.Write(b)
	}
	cw.buf.Write(b)
	if cw.buf.Len() >= cw.minSize {
		cw.commitCompressed()
	}
	return len(b), nil
}

// Flush forces a decision: bytes still buffered below the threshold
// flow out uncompressed because compressing under-sized output wastes
// CPU. After the decision, the underlying flusher is invoked so
// streaming downstreams keep working.
func (cw *compressWriter) Flush() {
	if !cw.decided {
		cw.commitPassthrough()
	} else if cw.compress {
		_ = cw.cmp.Flush()
	}
	if f, ok := cw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// shouldSkipImmediately checks the headers the upstream handler set
// before any body bytes - already-encoded responses, declared-small
// Content-Length, or skip-listed Content-Type all bypass the encoder.
func (cw *compressWriter) shouldSkipImmediately() bool {
	h := cw.Header()
	if h.Get("Content-Encoding") != "" {
		return true
	}
	if cl := h.Get("Content-Length"); cl != "" {
		if n, err := strconv.Atoi(cl); err == nil && n < cw.minSize {
			return true
		}
	}
	ct := h.Get("Content-Type")
	if ct == "" {
		return false
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	for _, t := range cw.skipTypes {
		if strings.HasPrefix(ct, t) {
			return true
		}
	}
	return false
}

func (cw *compressWriter) commitPassthrough() {
	cw.decided = true
	cw.compress = false
	cw.ResponseWriter.WriteHeader(cw.statusCode)
	if cw.buf.Len() > 0 {
		_, _ = cw.ResponseWriter.Write(cw.buf.Bytes())
		cw.buf.Reset()
	}
}

func (cw *compressWriter) commitCompressed() {
	cw.decided = true
	cw.compress = true
	h := cw.Header()
	h.Set("Content-Encoding", cw.encoding)
	h.Del("Content-Length") // unknown after compression
	h.Del("Accept-Ranges")  // byte ranges meaningless after compression
	cw.ResponseWriter.WriteHeader(cw.statusCode)

	switch cw.encoding {
	case "gzip":
		gz := cw.gzipPool.Get().(*gzip.Writer)
		gz.Reset(cw.ResponseWriter)
		cw.cmp = gz
	case "deflate":
		df := cw.deflatePool.Get().(*flate.Writer)
		df.Reset(cw.ResponseWriter)
		cw.cmp = df
	}
	if cw.buf.Len() > 0 {
		_, _ = cw.cmp.Write(cw.buf.Bytes())
		cw.buf.Reset()
	}
}

// Close finalises the response: flush+return any active encoder to the
// pool, or release the buffered passthrough bytes when the handler
// wrote less than the threshold.
func (cw *compressWriter) Close() {
	if !cw.decided {
		if cw.headerSet {
			cw.commitPassthrough()
		}
		return
	}
	if !cw.compress {
		return
	}
	_ = cw.cmp.Close()
	switch v := cw.cmp.(type) {
	case *gzip.Writer:
		cw.gzipPool.Put(v)
	case *flate.Writer:
		cw.deflatePool.Put(v)
	}
	cw.cmp = nil
}
