package semantic

import (
	"path/filepath"
	"strings"
	"testing"
)

// A `file` below the request's top level is rejected; top-level and mixin files are not.
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
}

// A `file` in a map is rejected even at the request's top level.
func TestRequestFileInMapRejected(t *testing.T) {
	d := expectError(t, `package design
type UploadReq { f file  byName map<string, file> }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`, CodeFilePosition)
	expectMessage(t, d, "UploadReq.byName")
}

// A `file` anywhere in a response, an error body or an event payload is
// rejected at the clause or error field that carries it, naming where it sits.
func TestFileOutsideRequestRejected(t *testing.T) {
	for label, c := range map[string]struct{ src, at string }{
		"response": {`type Resp { f file  ok bool }
service S { get A /a { response Resp } }`, "Resp.f"},
		"response nested": {`type Att { data file }
type Resp { att Att }
service S { get A /a { response Resp } }`, "Resp.att.data"},
		"response mixin": {`type Bits { data file }
type Resp { Bits  ok bool }
service S { get A /a { response Resp } }`, "Resp.data"},
		"response array": {`type Resp { files file[] }
service S { get A /a { response Resp } }`, "Resp.files"},
		"response map": {`type Resp { byName map<string, file> }
service S { get A /a { response Resp } }`, "Resp.byName"},
		"raw response": {`type Resp { f file }
service S { @rawResponse get A /a { response Resp } }`, "Resp.f"},
		"echo": {`type Profile { avatar file  name string }
service S { post Up /up { request Profile  response Profile } }`, "Profile.avatar"},
		"error body": {`error BadRequest Oops { f file }`, "Oops.f"},
		"error nested": {`type Att { data file }
error BadRequest Oops { att Att }`, "Oops.att.data"},
		"payload": {`type P { f file  id string }
event Placed { payload P }`, "P.f"},
		"payload array": {`type Att { data file }
type P { att Att[] }
event Placed { payload P[] }`, "P.att.data"},
	} {
		t.Run(label, func(t *testing.T) {
			d := expectError(t, "package design\n"+c.src, CodeFilePosition)
			expectMessage(t, d, c.at, "only a request")
		})
	}
}

// A `file` a generic instance takes as an argument is found wherever the
// instance sits; a generic request or mixin that takes it to its top level is
// accepted.
func TestFileThroughGenericInstance(t *testing.T) {
	const box = "package design\ntype Box<T> { v T }\ntype Resp { ok bool }\n"
	for label, c := range map[string]struct{ src, at string }{
		"request field": {`type R { f file  b Box<file> }
service S { post A /a { request R  response Resp } }`, "R.b"},
		"nested request field": {`type Meta { b Box<file[]> }
type R { f file  m Meta }
service S { post A /a { request R  response Resp } }`, "Meta.b"},
		"response": {`type Out { b Box<file> }
service S { get A /a { response Out } }`, "Out.b"},
		"payload": {`type P { b Box<map<string, file>> }
event Placed { payload P }`, "P.b"},
	} {
		t.Run(label, func(t *testing.T) {
			d := expectError(t, box+c.src, CodeFilePosition)
			expectMessage(t, d, c.at)
		})
	}
	for label, src := range map[string]string{
		"generic request": `type Up<T> { f T  name string }
service S { post A /a { request Up<file>  response Resp } }`,
		"generic mixin": `type R { Box<file>  name string }
service S { post A /a { request R  response Resp } }`,
	} {
		t.Run(label, func(t *testing.T) {
			expectNoCode(t, box+src, CodeFilePosition)
		})
	}
}

// A `file` a clause's own generic instance takes as an argument is rejected
// at the field its type parameter types; an argument no field takes is not.
func TestFileThroughClauseTypeArguments(t *testing.T) {
	const decls = "package design\ntype Box<T> { v T }\ntype Page<T> { items T[]  n int }\ntype Req { id string }\n"
	for label, c := range map[string]struct{ src, at string }{
		"response":          {`service S { get A /a { request Req  response Box<file> } }`, "at Box<file>.v,"},
		"response array":    {`service S { get A /a { request Req  response Page<file> } }`, "at Page<file>.items,"},
		"response instance": {`service S { get A /a { request Req  response Page<Box<file>> } }`, "at Page<Box<file>>.items,"},
		"payload":           {`event Placed { payload Box<file> }`, "at Box<file>.v,"},
		"error mixin":       {`error Conflict Dup { Box<file> }`, "at Dup.v,"},
	} {
		t.Run(label, func(t *testing.T) {
			d := expectError(t, decls+c.src, CodeFilePosition)
			expectMessage(t, d, c.at, "only a request")
		})
	}
	expectNoCode(t, `package design
type Tagged<T> { n int }
type Req { id string }
service S { get A /a { request Req  response Tagged<file> } }`, CodeFilePosition)
}

// A response type another package declares is checked at the response
// clause that names it.
func TestFileInCrossPackageResponseRejected(t *testing.T) {
	root, files := projectFixture(t, map[string]string{
		"shared/s.craftgo": "package shared\ntype Att { data file }\ntype Resp { att Att }",
		"api.craftgo":      "package api\nimport \"shared\"\nservice S { get A /a { response shared.Resp } }",
	})
	_, diags := AnalyzeProject(files, Options{DesignRoot: root})
	d := findCode(diags, CodeFilePosition)
	if d == nil || d.Pos.Filename != filepath.Join(root, "api.craftgo") || !strings.Contains(d.Msg, "shared.Resp.att.data") {
		t.Fatalf("want the file reported at api's response clause through shared.Resp.att.data, got %v", diags)
	}
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
