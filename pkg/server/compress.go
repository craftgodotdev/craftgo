package server

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"iter"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// CompressOptions tunes [Compress].
type CompressOptions struct {
	// MinSize is the smallest body, in bytes, that is compressed; 0 means 1024.
	MinSize int
	// Level is the gzip or flate level; 0 means gzip.DefaultCompression.
	Level int
	// SkipTypes are Content-Type prefixes never compressed; nil keeps the defaults, empty skips none.
	SkipTypes []string
}

// defaultSkipTypes are the Content-Type prefixes skipped by default: media, archives and fonts.
var defaultSkipTypes = []string{
	"image/", "video/", "audio/",
	"application/zip", "application/gzip", "application/x-gzip",
	"application/x-bzip2", "application/x-7z-compressed",
	"application/x-rar-compressed", "application/x-tar",
	"font/woff", "font/woff2",
}

// Compress gzip- or deflate-encodes responses for clients that accept it, except HEAD
// responses, already-encoded ones, bodies below MinSize and skipped types. Every response
// gets Vary: Accept-Encoding. Only the first opts value is read.
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

// acceptedCodings yields the lower-cased codings of an Accept-Encoding value in header order,
// skipping q=0 ones (an explicit refusal, RFC 7231 §5.3.1); `*` has no wildcard meaning.
func acceptedCodings(accept string) iter.Seq[string] {
	return func(yield func(string) bool) {
		if accept == "" {
			return
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
			if !yield(strings.ToLower(strings.TrimSpace(token))) {
				return
			}
		}
	}
}

// negotiateEncoding returns the first of "gzip" and "deflate" that accept lists, or "";
// header order wins over q-values.
func negotiateEncoding(accept string) string {
	for coding := range acceptedCodings(accept) {
		switch coding {
		case "gzip", "deflate":
			return coding
		}
	}
	return ""
}

// AcceptsEncoding reports whether r's Accept-Encoding lists coding ("zstd", "gzip", ...),
// case-insensitively, with a non-zero quality; `*` does not count.
func AcceptsEncoding(r *http.Request, coding string) bool {
	return acceptsCoding(r.Header.Get("Accept-Encoding"), coding)
}

// acceptsCoding is the header-value form of AcceptsEncoding.
func acceptsCoding(accept, coding string) bool {
	coding = strings.ToLower(strings.TrimSpace(coding))
	if coding == "" {
		return false
	}
	for c := range acceptedCodings(accept) {
		if c == coding {
			return true
		}
	}
	return false
}

// qualityIsZero reports whether an Accept-Encoding parameter list sets q=0 (or 0.0, ...).
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

// compressWriter buffers the response head until it can choose between compressing and
// passing through.
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

// WriteHeader records the first final status, sent once the choice is made; a status that is not
// final goes straight out.
func (cw *compressWriter) WriteHeader(code int) {
	if cw.headerSet {
		return
	}
	if !finalStatus(code) {
		cw.ResponseWriter.WriteHeader(code)
		return
	}
	cw.statusCode = code
	cw.headerSet = true
}

func (cw *compressWriter) Unwrap() http.ResponseWriter { return cw.ResponseWriter }

// Committed reports whether the status is fixed, even while the body is still buffered.
func (cw *compressWriter) Committed() bool { return cw.headerSet }

// Write buffers until minSize bytes arrive, then compresses; headers that rule compression
// out send the body straight through.
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

// Flush sends the head, 200 unless a status was written, and a still-buffered body
// uncompressed, or flushes the encoder, then flushes the wrapped writer.
func (cw *compressWriter) Flush() {
	if !cw.headerSet {
		cw.WriteHeader(http.StatusOK)
	}
	if !cw.decided {
		cw.commitPassthrough()
	} else if cw.compress {
		_ = cw.cmp.Flush()
	}
	if f, ok := cw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// shouldSkipImmediately reports whether the headers rule compression out: a Content-Encoding,
// a Content-Length below minSize or a skipped Content-Type.
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

// Close sends a still-buffered body uncompressed, or closes the encoder and returns it to its pool.
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
