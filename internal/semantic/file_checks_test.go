package semantic

import "testing"

// A `file` field nested below the top level of a request body cannot be bound:
// the multipart binder reads only the resolved top-level request fields, so the
// `*multipart.FileHeader` stays nil and the upload is silently lost while gen
// and `go build` both succeed. Reject the nesting. A top-level `file` (direct
// or flattened in via a mixin) and a `file` carried in a response (including a
// type echoed back as its own response) stay valid.
func TestNestedRequestFileRejected(t *testing.T) {
	expectError(t, `package design
type Wrap { data file @form }
type UploadReq { wrapper Wrap }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`, CodeFilePosition)

	// Reached two levels down.
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
	// Top-level file binds directly.
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
	// A file echoed back in a response is an established modelling pattern.
	mustNoFilePosition("echo", `package design
type Profile { avatar file @form  name string }
service S { post Up /up { request Profile  response Profile } }`)
}

// A 1-D `file[]` binds every repeated multipart part into a
// []*multipart.FileHeader; a multi-dimensional `file[][]` has no multipart
// encoding and is rejected at gen time rather than emitting non-compiling Go.
func TestMultiDimFileArrayRejected(t *testing.T) {
	expectError(t, `package design
type UploadReq { grid file[][]  name string @form }
type Resp { ok bool }
service S { post Up /up { request UploadReq  response Resp } }`, CodeFilePosition)

	// 1-D file[] is accepted - auto-form and explicit @form alike.
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
