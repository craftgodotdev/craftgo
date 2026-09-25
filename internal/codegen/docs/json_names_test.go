package docs

import (
	"maps"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/craftgodotdev/craftgo/internal/config"
)

// The body of a mixed request and of a header-split response key their
// properties, and list them as required, by the @json name.
func TestOperationBodiesKeyPropertiesByJSONName(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
type Req {
	id       string  @path
	fullName string  @json("full_name")
	nick     string? @json("nick_name")
}
type Resp {
	trace       string @header("X-Trace")
	displayName string @json("display_name")
}
service S {
	post Create /things/{id} { request Req  response Resp }
}`,
	}, &config.Config{})
	for name, want := range map[string]struct{ props, required []string }{
		"CreateReqBody":  {props: []string{"full_name", "nick_name"}, required: []string{"full_name"}},
		"CreateRespBody": {props: []string{"display_name"}, required: []string{"display_name"}},
	} {
		s := doc.Components.Schemas[name]
		if s == nil || s.Value == nil {
			t.Fatalf("no %s component", name)
		}
		if got := slices.Sorted(maps.Keys(s.Value.Properties)); !slices.Equal(got, want.props) {
			t.Errorf("%s properties = %v, want %v", name, got, want.props)
		}
		if got := s.Value.Required; !slices.Equal(got, want.required) {
			t.Errorf("%s required = %v, want %v", name, got, want.required)
		}
	}
}

// A cross-field fragment names each member by the key its body carries: the
// @json name, a member a mixin promotes included.
func TestCrossFieldFragmentsNameJSONKeys(t *testing.T) {
	doc := genDoc(t, map[string]string{
		"a/a.craftgo": `package a
type Pair {
	email string? @json("e_mail")
	phone string?
}
@requiresOneOf(email, phone)
type Contact {
	Pair
	note string
}
@requiresOneOf(mail, sms)
type Direct {
	id   string  @path
	mail string? @json("mail_addr")
	sms  string?
}
service S {
	post C /c { request Contact  response Contact }
	post D /d/{id} { request Direct  response Direct }
}`,
	}, &config.Config{})
	for name, want := range map[string][]string{
		"Contact":  {"e_mail", "phone"},
		"Direct":   {"mail_addr", "sms"},
		"DReqBody": {"mail_addr", "sms"},
	} {
		s := doc.Components.Schemas[name]
		if s == nil || s.Value == nil {
			t.Fatalf("no %s component", name)
		}
		if got := fragmentKeys(s.Value); !slices.Equal(got, want) {
			t.Errorf("%s cross-field fragment keys = %v, want %v", name, got, want)
		}
	}
	host := doc.Components.Schemas["DReqBody"].Value.AllOf[0].Value
	if got := slices.Sorted(maps.Keys(host.Properties)); !slices.Equal(got, []string{"mail_addr", "sms"}) {
		t.Errorf("DReqBody properties = %v, want the keys its fragment names", got)
	}
}

// fragmentKeys returns the member keys the `anyOf` and `not` fragments of
// s's allOf require, sorted.
func fragmentKeys(s *openapi3.Schema) []string {
	var out []string
	add := func(branch *openapi3.SchemaRef) {
		if branch != nil && branch.Value != nil {
			out = append(out, branch.Value.Required...)
		}
	}
	for _, member := range s.AllOf {
		if member.Value == nil {
			continue
		}
		for _, branch := range member.Value.AnyOf {
			add(branch)
		}
		add(member.Value.Not)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
