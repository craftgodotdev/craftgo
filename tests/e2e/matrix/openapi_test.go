package matrix

import (
	"encoding/json"
	"maps"
	"mime/multipart"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	bindings "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/bindings"
	combine "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/combine"
)

func readOpenAPI(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(here), "docs/openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestOpenAPI_DocumentShape(t *testing.T) {
	doc := readOpenAPI(t)
	for _, want := range []string{
		"openapi: 3.1.0",
		"AcctUser:",
		"AcctCreateUserReq:",
		// GetUser/CreateUser collide with UserService's, so the operationId
		// is service-prefixed.
		"operationId: AccountUserServiceGetUser",
		"operationId: AccountUserServiceCreateUser",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("openapi.yaml missing %q", want)
		}
	}
}

func TestOpenAPI_MultiServiceOperationIDDisambiguation(t *testing.T) {
	// OrdersService and CatalogService both declare Ping.
	doc := readOpenAPI(t)
	for _, want := range []string{
		"operationId: OrdersServicePing",
		"operationId: CatalogServicePing",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("openapi.yaml missing disambiguated %q", want)
		}
	}
}

func TestOpenAPI_SecuritySchemeEmitted(t *testing.T) {
	doc := readOpenAPI(t)
	if !strings.Contains(doc, "ProfileAuth:") {
		t.Error("securitySchemes.ProfileAuth missing")
	}
	if !strings.Contains(doc, "- ProfileAuth") {
		t.Error("no operation references the ProfileAuth security requirement")
	}
}

// pathBlock returns the YAML block of the path item holding operation opID;
// each raw-modes path holds one operation.
func pathBlock(t *testing.T, doc, opID string) string {
	t.Helper()
	for _, block := range strings.Split(doc, "\n  /") {
		if strings.Contains(block, "operationId: "+opID+"\n") {
			return block
		}
	}
	t.Fatalf("operation %s not found in openapi.yaml", opID)
	return ""
}

// A block on a raw side is documented like a typed one, a raw side without
// one stays */*, and a raw response documents 200 unless @status sets it.
func TestOpenAPI_RawModesContracts(t *testing.T) {
	doc := readOpenAPI(t)

	pt := pathBlock(t, doc, "PtBlocks")
	for _, want := range []string{"in: path", "name: verbose", "in: query", "$ref: '#/components/schemas/PtBlocksRespBody'", `"200"`} {
		if !strings.Contains(pt, want) {
			t.Errorf("PtBlocks missing %q:\n%s", want, pt)
		}
	}
	if strings.Contains(pt, "'*/*'") {
		t.Errorf("PtBlocks must not fall back to */*")
	}

	bare := pathBlock(t, doc, "PtPlain")
	if !strings.Contains(bare, "'*/*'") || strings.Contains(bare, "$ref") {
		t.Errorf("bare passthrough keeps */*:\n%s", bare)
	}

	rr := pathBlock(t, doc, "RrReq")
	for _, want := range []string{`"201"`, "$ref: '#/components/schemas/RrReqRespBody'", "Location:", "$ref: '#/components/schemas/RrReqReqBody'"} {
		if !strings.Contains(rr, want) {
			t.Errorf("RrReq missing %q:\n%s", want, rr)
		}
	}

	rrNoReq := pathBlock(t, doc, "RrNoReq")
	if !strings.Contains(rrNoReq, "'*/*'") {
		t.Errorf("raw response without a block keeps */*:\n%s", rrNoReq)
	}

	rq := pathBlock(t, doc, "RqResp")
	if !strings.Contains(rq, `"201"`) || !strings.Contains(rq, "$ref: '#/components/schemas/RqRespRespBody'") {
		t.Errorf("typed response after a raw request keeps the verb default 201:\n%s", rq)
	}

	rqDocs := pathBlock(t, doc, "RqDocsReq")
	if !strings.Contains(rqDocs, "multipart/form-data") || !strings.Contains(rqDocs, "format: binary") {
		t.Errorf("docs-only multipart contract must be advertised:\n%s", rqDocs)
	}

	rqBare := pathBlock(t, doc, "RqNoResp")
	if !strings.Contains(rqBare, "in: path") || !strings.Contains(rqBare, `"204"`) {
		t.Errorf("raw request without a block: bare path param + 204:\n%s", rqBare)
	}

	if !strings.Contains(doc, "PageOfRqItem:") {
		t.Errorf("cross-package generic response on a raw request must register its instance component")
	}
}

// Every operation declares each variable of its path, and only those, as a
// path parameter: a raw request's route, @prefix included, too.
func TestOpenAPI_EveryPathVariableIsDeclared(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				In   string `yaml:"in"`
				Name string `yaml:"name"`
			} `yaml:"parameters"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal([]byte(readOpenAPI(t)), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Paths["/tenant/{tenantID}/export/{format}"]["get"]; !ok {
		t.Fatal("the raw ExportTenantItems operation is missing")
	}
	vars := regexp.MustCompile(`\{([^}]+)\}`)
	for path, item := range doc.Paths {
		var want []string
		for _, m := range vars.FindAllStringSubmatch(path, -1) {
			want = append(want, m[1])
		}
		slices.Sort(want)
		for verb, op := range item {
			var got []string
			for _, p := range op.Parameters {
				if p.In == "path" {
					got = append(got, p.Name)
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("%s %s declares path parameters %v, want %v", strings.ToUpper(verb), path, got, want)
			}
		}
	}
}

// No parameter and no response header admits null: each is sent or not, and
// an optional one is only left out of `required`.
func TestOpenAPI_ParametersAndHeadersAreNeverNull(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				In       string `yaml:"in"`
				Name     string `yaml:"name"`
				Required bool   `yaml:"required"`
				Schema   any    `yaml:"schema"`
			} `yaml:"parameters"`
			Responses map[string]struct {
				Headers map[string]struct {
					Schema any `yaml:"schema"`
				} `yaml:"headers"`
			} `yaml:"responses"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal([]byte(readOpenAPI(t)), &doc); err != nil {
		t.Fatal(err)
	}
	var admitsNull func(any) bool
	admitsNull = func(s any) bool {
		switch v := s.(type) {
		case map[string]any:
			for k, x := range v {
				if k == "type" && (x == "null" || slices.Contains(toList(x), "null")) || admitsNull(x) {
					return true
				}
			}
		case []any:
			return slices.ContainsFunc(v, admitsNull)
		}
		return false
	}
	optional := map[string]bool{}
	for path, item := range doc.Paths {
		for verb, op := range item {
			for _, p := range op.Parameters {
				if admitsNull(p.Schema) {
					t.Errorf("%s %s: %s parameter %s admits null: %v", strings.ToUpper(verb), path, p.In, p.Name, p.Schema)
				}
				if path == "/bindings/optional-wire" {
					optional[p.In+" "+p.Name] = !p.Required
				}
			}
			for code, resp := range op.Responses {
				for name, h := range resp.Headers {
					if admitsNull(h.Schema) {
						t.Errorf("%s %s: response %s header %s admits null: %v", strings.ToUpper(verb), path, code, name, h.Schema)
					}
				}
			}
		}
	}
	if want := map[string]bool{"header X-Trace": true, "cookie theme": true}; !maps.Equal(optional, want) {
		t.Errorf("GetOptionalWire parameters (optional) = %v, want %v", optional, want)
	}
}

// toList returns v as a list of strings, empty for anything else.
func toList(v any) []string {
	items, _ := v.([]any)
	var out []string
	for _, x := range items {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// The XRefsService of xrefs and the one of xshared are both documented,
// each under its own tag; the method name both declare keeps each
// operation's body components apart, named after its package.
func TestOpenAPI_SameNamedServicesAreAllDocumented(t *testing.T) {
	doc := readOpenAPI(t)
	for opID, wants := range map[string][]string{
		"XRefsServiceGetItem":  {"- XRefsService"},
		"GetSharedOwner":       {"- xshared"},
		"XRefsServiceDescribe": {"- XRefsService", "$ref: '#/components/schemas/XrefsXRefsServiceDescribeRespBody'"},
		"DescribeShared": {
			"- xshared",
			"$ref: '#/components/schemas/XsharedXRefsServiceDescribeReqBody'",
			"$ref: '#/components/schemas/XsharedXRefsServiceDescribeRespBody'",
		},
	} {
		block := pathBlock(t, doc, opID)
		for _, want := range wants {
			if !strings.Contains(block, want) {
				t.Errorf("%s missing %q:\n%s", opID, want, block)
			}
		}
	}
}

// An @errors name two packages declare documents the error the analyser
// resolves it to, in the array form and from a package declaring neither.
func TestOpenAPI_ErrorsFollowTheMergedNames(t *testing.T) {
	doc := readOpenAPI(t)
	for opID, wants := range map[string][]string{
		"GetLost":    {"- $ref: '#/components/schemas/XrefsXLostErr'", "- $ref: '#/components/schemas/XsharedXLostErr'"},
		"LookupLost": {"$ref: '#/components/schemas/XrefsXLostErr'"},
	} {
		block := pathBlock(t, doc, opID)
		for _, want := range append(wants, `"404":`) {
			if !strings.Contains(block, want) {
				t.Errorf("%s missing %q:\n%s", opID, want, block)
			}
		}
	}
}

// Bodies sharing a status are documented as an anyOf, never a oneOf:
// RetryLater and Maintenance both send the {code, message} envelope, which
// would match both schemas of a oneOf and so fail it.
func TestOpenAPI_ResponsesSharingAStatusAreAnAnyOf(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema struct {
						OneOf []any `yaml:"oneOf"`
						AnyOf []struct {
							Ref string `yaml:"$ref"`
						} `yaml:"anyOf"`
					} `yaml:"schema"`
				} `yaml:"content"`
			} `yaml:"responses"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal([]byte(readOpenAPI(t)), &doc); err != nil {
		t.Fatal(err)
	}
	for path, item := range doc.Paths {
		for verb, op := range item {
			for code, resp := range op.Responses {
				if len(resp.Content["application/json"].Schema.OneOf) > 0 {
					t.Errorf("%s %s: response %s is a oneOf", strings.ToUpper(verb), path, code)
				}
			}
		}
	}
	var refs []string
	for _, branch := range doc.Paths["/bindings/service-status"]["get"].Responses["503"].Content["application/json"].Schema.AnyOf {
		refs = append(refs, branch.Ref)
	}
	if want := []string{"#/components/schemas/RetryLaterErr", "#/components/schemas/MaintenanceErr"}; !slices.Equal(refs, want) {
		t.Errorf("GetServiceStatus 503 anyOf = %v, want %v", refs, want)
	}
	for _, err := range []error{bindings.NewRetryLaterErr(bindings.RetryLaterBody{}), bindings.NewMaintenanceErr()} {
		raw, merr := json.Marshal(err)
		if merr != nil {
			t.Fatal(merr)
		}
		var body map[string]string
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if keys := slices.Sorted(maps.Keys(body)); !slices.Equal(keys, []string{"code", "message"}) {
			t.Errorf("%T sends %s, want the {code, message} envelope", err, raw)
		}
	}
}

// schemaDoc is the part of a component schema the body-key checks read.
type schemaDoc struct {
	Properties map[string]any `yaml:"properties"`
	Required   []string       `yaml:"required"`
	AllOf      []schemaDoc    `yaml:"allOf"`
	AnyOf      []schemaDoc    `yaml:"anyOf"`
	Not        *schemaDoc     `yaml:"not"`
}

// readSchemas returns the component schemas of docs/openapi.yaml.
func readSchemas(t *testing.T) map[string]schemaDoc {
	t.Helper()
	var doc struct {
		Components struct {
			Schemas map[string]schemaDoc `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal([]byte(readOpenAPI(t)), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Components.Schemas
}

// An operation body beside a path id keys its properties and its
// @requiresOneOf members by their @json names, which the runtime decodes.
func TestOpenAPI_BodyKeysAreJSONNames(t *testing.T) {
	schemas := readSchemas(t)
	want := []string{"backup_email", "primary_email"}

	req := schemas["ValidateRenamedReqBody"]
	if len(req.AllOf) != 2 {
		t.Fatalf("ValidateRenamedReqBody: want the body and one cross-field fragment, got %+v", req)
	}
	if got := slices.Sorted(maps.Keys(req.AllOf[0].Properties)); !slices.Equal(got, want) {
		t.Errorf("ValidateRenamedReqBody properties = %v, want %v", got, want)
	}
	var members []string
	for _, branch := range req.AllOf[1].AnyOf {
		members = append(members, branch.Required...)
	}
	slices.Sort(members)
	if !slices.Equal(members, want) {
		t.Errorf("ValidateRenamedReqBody @requiresOneOf names %v, want %v", members, want)
	}
	for _, key := range members {
		var body combine.PairsRenamed
		if err := json.Unmarshal([]byte(`{"`+key+`": "a@b.c"}`), &body); err != nil {
			t.Fatal(err)
		}
		if err := body.Validate(); err != nil {
			t.Errorf("a body carrying only the documented %q fails the group: %v", key, err)
		}
	}

	resp := schemas["ValidateRenamedRespBody"]
	if got := slices.Sorted(maps.Keys(resp.Properties)); !slices.Equal(got, []string{"primary_email"}) {
		t.Errorf("ValidateRenamedRespBody properties = %v, want [primary_email]", got)
	}
}

// A body listed in place carries the @requiresOneOf of a mixin it embeds,
// which the validator runs: the JSON body beside a path id and the multipart
// body beside a file.
func TestOpenAPI_InlineBodiesCarryMixinGroups(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			RequestBody struct {
				Content map[string]struct {
					Schema schemaDoc `yaml:"schema"`
				} `yaml:"content"`
			} `yaml:"requestBody"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal([]byte(readOpenAPI(t)), &doc); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]schemaDoc{
		"ValidateNestedReqBody": readSchemas(t)["ValidateNestedReqBody"],
		"UploadPairs multipart": doc.Paths["/combine/pairs/upload"]["post"].RequestBody.Content["multipart/form-data"].Schema,
	} {
		var members []string
		for _, part := range body.AllOf {
			for _, branch := range part.AnyOf {
				members = append(members, branch.Required...)
			}
		}
		if !slices.Equal(members, []string{"a", "b"}) {
			t.Errorf("%s @requiresOneOf names %v, want [a b]", name, members)
		}
	}
	if err := (&combine.PairsNested{Note: "n"}).Validate(); err == nil {
		t.Error("PairsNested without a or b passes validation")
	}
	if err := (&combine.PairsUpload{Doc: &multipart.FileHeader{}}).Validate(); err == nil {
		t.Error("PairsUpload without a or b passes validation")
	}
}

// @mutuallyExclusive(email, sms, push) admits at most one channel: the
// validator rejects any two, and each body schema carrying the group forbids
// every pair.
func TestOpenAPI_MutuallyExclusiveForbidsEveryPair(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			RequestBody struct {
				Content map[string]struct {
					Schema schemaDoc `yaml:"schema"`
				} `yaml:"content"`
			} `yaml:"requestBody"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal([]byte(readOpenAPI(t)), &doc); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"email", "sms"}, {"email", "push"}, {"sms", "push"}}
	for name, body := range map[string]schemaDoc{
		"NotifyChannels":         readSchemas(t)["NotifyChannels"],
		"UploadNotify multipart": doc.Paths["/combine/pairs/notify/upload"]["post"].RequestBody.Content["multipart/form-data"].Schema,
	} {
		var pairs [][]string
		for _, part := range body.AllOf {
			if part.Not != nil {
				for _, branch := range part.Not.AnyOf {
					pairs = append(pairs, branch.Required)
				}
			}
		}
		if !slices.EqualFunc(pairs, want, slices.Equal) {
			t.Errorf("%s forbids the pairs %v, want %v", name, pairs, want)
		}
	}
	e, s, p := "e", "s", "p"
	for _, v := range []combine.NotifyChannels{{Email: &e, Sms: &s}, {Email: &e, Push: &p}, {Sms: &s, Push: &p}} {
		if err := v.Validate(); err == nil {
			t.Errorf("%+v passes validation", v)
		}
	}
	if err := (&combine.NotifyChannels{Push: &p}).Validate(); err != nil {
		t.Errorf("one channel fails validation: %v", err)
	}
}
