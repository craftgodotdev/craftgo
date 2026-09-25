package matrix

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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

// The XRefsService of xrefs and the one of xshared are both documented,
// each under its own tag.
func TestOpenAPI_SameNamedServicesAreAllDocumented(t *testing.T) {
	doc := readOpenAPI(t)
	for opID, tag := range map[string]string{
		"XRefsServiceGetItem": "- XRefsService",
		"GetSharedOwner":      "- xshared",
	} {
		if block := pathBlock(t, doc, opID); !strings.Contains(block, tag) {
			t.Errorf("%s is not tagged %q:\n%s", opID, tag, block)
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

// schemaDoc is the part of a component schema the body-key checks read.
type schemaDoc struct {
	Properties map[string]any `yaml:"properties"`
	Required   []string       `yaml:"required"`
	AllOf      []schemaDoc    `yaml:"allOf"`
	AnyOf      []schemaDoc    `yaml:"anyOf"`
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
