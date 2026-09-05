package matrix

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
		// GetUser/CreateUser collide with cornercase's UserService, so the
		// operationId is service-prefixed.
		"operationId: AccountUserServiceGetUser",
		"operationId: AccountUserServiceCreateUser",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("openapi.yaml missing %q", want)
		}
	}
}

func TestOpenAPI_MultiServiceOperationIDDisambiguation(t *testing.T) {
	// OrdersService and CatalogService both declare `Ping`; the operationIds
	// disambiguate by service.
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

// pathBlock returns the YAML block of the path item that carries opID (each
// raw-modes path declares a single operation, so the path block is the
// operation block).
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

// The raw modes: a request / response block on a raw side is the documented
// contract, emitted exactly like a typed one; no block keeps the untyped
// fallbacks; a raw response documents 200 unless @status says otherwise.
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
