package server

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/textproto"
)

// MultipartMemory is the most of a multipart form's files that generated handlers hold in memory;
// the rest spills to temporary files, whatever the route's body cap.
const MultipartMemory = 32 << 20

// LimitParts holds the parts of r's multipart/form-data body to limits, by form name, while they
// are read: once a part's content passes its limit, reading the body fails with the validation
// error "<name>: file size exceeds <limit> bytes", so a handler stops at the limit instead of
// spooling the whole part first. A request with another body is left as it is.
func LimitParts(r *http.Request, limits map[string]int64) {
	if len(limits) == 0 || r.Body == nil || r.Body == http.NoBody {
		return
	}
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
		return
	}
	r.Body = &partLimiter{
		ReadCloser: r.Body,
		delim:      []byte("\n--" + params["boundary"]),
		limits:     limits,
		// The leading newline lets the first delimiter, at the start of the body, match.
		pending: []byte{'\n'},
	}
}

// partTooLarge is the read error of a part past its limit.
type partTooLarge struct {
	name  string
	limit int64
}

func (e *partTooLarge) Error() string {
	return fmt.Sprintf("%s: file size exceeds %d bytes", e.name, e.limit)
}

// The states a partLimiter walks a multipart body through.
const (
	inPreamble  = iota // before the first delimiter
	inDelimLine        // after a delimiter, up to its line end
	inHeaders          // a part's header block
	inContent          // a part's content
	inEpilogue         // after the closing delimiter, or past what it can follow
)

// maxPartHeader is the largest delimiter line or header block a partLimiter reads; past it the
// body passes unwatched, for the multipart parser to refuse.
const maxPartHeader = 64 << 10

// partLimiter watches a multipart body as it is read and counts the content of each part whose
// form name has a limit. It follows the framing as the parser does: a delimiter is a newline, "--"
// and the boundary, then "--", a space or a line end; the first delimiter line ending in CRLF
// makes the carriage return before each later delimiter part of it.
type partLimiter struct {
	io.ReadCloser
	delim  []byte
	limits map[string]int64
	state  int
	// crlf reports that the first delimiter line ends in CRLF, which delimSeen says was read.
	crlf, delimSeen bool
	// pending holds the bytes not yet weighed: a possible delimiter's start, a line, a header block.
	pending []byte
	// name and limit are the current part's; count its content so far.
	name  string
	limit int64
	count int64
	// err is the failure a part past its limit left, which every later read returns.
	err error
}

// Read returns the body's bytes, but none past a part's limit: the read that passes it, and every
// one after, fails instead.
func (l *partLimiter) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	n, err := l.ReadCloser.Read(p)
	if n > 0 && l.state != inEpilogue {
		if l.err = l.weigh(p[:n]); l.err != nil {
			return 0, l.err
		}
	}
	return n, err
}

// weigh advances the framing over b, failing once the current part passes its limit.
func (l *partLimiter) weigh(b []byte) error {
	l.pending = append(l.pending, b...)
	for {
		switch l.state {
		case inPreamble, inContent:
			i := bytes.Index(l.pending, l.delim)
			if i < 0 || i+len(l.delim) == len(l.pending) {
				// All but a delimiter's possible start, and the carriage return before it, is content.
				keep := min(len(l.pending), len(l.delim)+1)
				if err := l.content(len(l.pending) - keep); err != nil {
					return err
				}
				l.pending = append(l.pending[:0], l.pending[len(l.pending)-keep:]...)
				return nil
			}
			switch l.pending[i+len(l.delim)] {
			case '-', ' ', '\t', '\r', '\n':
			default:
				// The boundary inside content, which the parser keeps as content.
				if err := l.content(i + 1); err != nil {
					return err
				}
				l.pending = l.pending[i+1:]
				continue
			}
			n := i
			if l.crlf && i > 0 && l.pending[i-1] == '\r' {
				n--
			}
			if err := l.content(n); err != nil {
				return err
			}
			l.pending = l.pending[i+len(l.delim):]
			l.state = inDelimLine
		case inDelimLine:
			i := bytes.IndexByte(l.pending, '\n')
			if i < 0 {
				if len(l.pending) > maxPartHeader {
					l.state = inEpilogue
				}
				return nil
			}
			if !l.delimSeen {
				l.crlf, l.delimSeen = i > 0 && l.pending[i-1] == '\r', true
			}
			closing := bytes.HasPrefix(l.pending, []byte("--"))
			l.pending = l.pending[i+1:]
			l.state = inHeaders
			if closing {
				l.state = inEpilogue
				return nil
			}
		case inHeaders:
			end, size := headerEnd(l.pending)
			if end < 0 {
				if len(l.pending) > maxPartHeader {
					l.state = inEpilogue
				}
				return nil
			}
			l.startPart(l.pending[:end])
			l.pending = l.pending[end+size:]
			l.state = inContent
		default:
			return nil
		}
	}
}

// content counts n bytes of the current part's content, failing past its limit.
func (l *partLimiter) content(n int) error {
	if l.state != inContent || l.limit <= 0 {
		return nil
	}
	l.count += int64(n)
	if l.count > l.limit {
		return &partTooLarge{name: l.name, limit: l.limit}
	}
	return nil
}

// startPart reads a part's header block and takes the limit of its form name.
func (l *partLimiter) startPart(header []byte) {
	l.name, l.limit, l.count = "", 0, 0
	block := io.MultiReader(bytes.NewReader(header), bytes.NewReader([]byte("\r\n\r\n")))
	h, err := textproto.NewReader(bufio.NewReader(block)).ReadMIMEHeader()
	if err != nil {
		return
	}
	_, params, err := mime.ParseMediaType(h.Get("Content-Disposition"))
	if err != nil {
		return
	}
	l.name = params["name"]
	l.limit = l.limits[l.name]
}

// headerEnd returns where the header block at the start of b ends and the size of the blank line
// that ends it; -1 while b holds no blank line.
func headerEnd(b []byte) (end, size int) {
	if bytes.HasPrefix(b, []byte("\r\n")) {
		return 0, 2
	}
	if bytes.HasPrefix(b, []byte("\n")) {
		return 0, 1
	}
	crlf, lf := bytes.Index(b, []byte("\r\n\r\n")), bytes.Index(b, []byte("\n\n"))
	switch {
	case crlf >= 0 && (lf < 0 || crlf < lf):
		return crlf, 4
	case lf >= 0:
		return lf, 2
	}
	return -1, 0
}
