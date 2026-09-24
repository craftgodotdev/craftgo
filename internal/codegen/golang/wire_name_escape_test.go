package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A quote in a wire name is escaped in the validator's error message.
func TestValidateEscapesWireNameInMessage(t *testing.T) {
	src := runValidateGen(t, `package design
type Req { id string @header("a\"b") @minLength(1) }`)
	if !strings.Contains(src, `a\"b: `) {
		t.Errorf("wire name with a quote must be escaped in the validate message:\n%s", src)
	}
}

// A quote in a @form name is escaped in the multipart FormFile key.
func TestGenerateTransportMultipartEscapesFormName(t *testing.T) {
	src := `package design
type UploadReq { avatar file @form("a\"b") }
service S { post Up /up { request UploadReq } }`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := generateTransport(pkg, sampleConfig(), root, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "internal/transport/s/up.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	mustParseGo(t, got)
	if !strings.Contains(got, `r.FormFile("a\"b")`) {
		t.Errorf("multipart form name with a quote must be emitted as a quoted literal:\n%s", got)
	}
}
