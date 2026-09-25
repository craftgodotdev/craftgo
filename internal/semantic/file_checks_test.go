package semantic

import (
	"strings"
	"testing"
)

// A `file` below the request's top level is rejected; top-level, mixin and response files are not.
func TestNestedRequestFileRejected(t *testing.T) {
	expectError(t, `package design
type Wrap { data file @form }
type UploadReq { wrapper Wrap }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`, CodeFilePosition)

	// Two levels down.
	expectError(t, `package design
type Leaf { data file }
type Mid { leaf Leaf }
type UploadReq { mid Mid }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`, CodeFilePosition)

	mustNoFilePosition := func(label, src string) {
		t.Helper()
		_, diags := Analyze(parseFiles(t, src))
		if d := findCode(diags, CodeFilePosition); d != nil {
			t.Errorf("%s: unexpected file-position rejection: %s", label, d.Msg)
		}
	}
	mustNoFilePosition("top-level", `package design
type UploadReq { f file @form  name string }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`)
	// A mixin flattens its file into the host's top level.
	mustNoFilePosition("mixin", `package design
type Bits { f file @form }
type UploadReq { Bits  name string }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`)
	// A type holding a file may also be the response.
	mustNoFilePosition("echo", `package design
type Profile { avatar file @form  name string }
service S { post Up /up { request Profile  response Profile } }`)
}

// A `file` nested in a struct another package declares is rejected too, and
// in a request type another package declares.
func TestNestedRequestFileAcrossPackagesRejected(t *testing.T) {
	for label, api := range map[string]string{
		"nested struct": "type UploadReq { att shared.Attachment }\nservice S { post Up /up { request UploadReq  response Resp } }",
		"request type":  "service S { post Up /up { request shared.UploadReq  response Resp } }",
	} {
		root, files := projectFixture(t, map[string]string{
			"shared/s.craftgo": "package shared\ntype Attachment { data file }\ntype UploadReq { att Attachment }",
			"api.craftgo":      "package api\nimport \"shared\"\ntype Resp { ok bool }\n" + api,
		})
		_, diags := AnalyzeProject(files, Options{DesignRoot: root})
		if d := findCode(diags, CodeFilePosition); d == nil || !strings.Contains(d.Msg, "UploadReq.att") {
			t.Errorf("%s: want the nested file reported through UploadReq.att, got %v", label, diags)
		}
	}
}

// A `file[][]` field is rejected; a `file[]` field is accepted.
func TestMultiDimFileArrayRejected(t *testing.T) {
	expectError(t, `package design
type UploadReq { grid file[][]  name string @form }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`, CodeFilePosition)

	// With and without an explicit @form.
	for label, src := range map[string]string{
		"auto": `package design
type UploadReq { files file[]  name string @form }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`,
		"explicit": `package design
type UploadReq { files file[] @form  name string @form }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`,
	} {
		if _, diags := Analyze(parseFiles(t, src)); findCode(diags, CodeFilePosition) != nil {
			t.Errorf("%s file[]: unexpected file-position rejection", label)
		}
	}
}
