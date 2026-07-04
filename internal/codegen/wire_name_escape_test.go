package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A wire name (`@query`/`@header`/`@cookie`/`@form` argument) is a
// user-controlled string that may contain a double quote or backslash. It is
// embedded into the generated validator's fmt.Errorf message and the multipart
// binder's FormFile key, so a raw interpolation emits an unterminated Go string
// literal that go/format rejects - aborting gen with an error pointing at code
// the user never wrote. These tests pin the escaping. Both harnesses parse the
// generated output, so a broken literal fails by construction.

func TestValidateEscapesWireNameInMessage(t *testing.T) {
	src := runValidateGen(t, `package design
type Req { id string @header("a\"b") @minLength(1) }`)
	// errSubject prefixes the (escaped) wire name: `"a\"b: length ..."`.
	if !strings.Contains(src, `a\"b: `) {
		t.Errorf("wire name with a quote must be escaped in the validate message:\n%s", src)
	}
}

func TestGenerateTransportMultipartEscapesFormName(t *testing.T) {
	src := `package design
type UploadReq { avatar file @form("a\"b") }
service S { post Up /up { request UploadReq } }`
	pkg := analyze(t, src)
	root := t.TempDir()
	if err := GenerateTransport(pkg, sampleConfig(), root); err != nil {
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
