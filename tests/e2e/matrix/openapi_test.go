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
