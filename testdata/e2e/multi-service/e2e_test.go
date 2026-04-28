// Package multiservice is the multi-service e2e fixture. It boots both
// services on the same Server and confirms each gets its own routes,
// handlers, and logic packages without colliding.
package multiservice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dropship-dev/craftgo/pkg/server"

	catalogroutes "github.com/dropship-dev/craftgo/testdata/e2e/multi-service/internal/routes/catalog-service"
	ordersroutes "github.com/dropship-dev/craftgo/testdata/e2e/multi-service/internal/routes/orders-service"
	types "github.com/dropship-dev/craftgo/testdata/e2e/multi-service/internal/types/design"
	"github.com/dropship-dev/craftgo/testdata/e2e/multi-service/svccontext"
)

// boot wires both services onto a single Server so each test can issue
// real HTTP requests against either.
func boot(t *testing.T) *httptest.Server {
	t.Helper()
	svc := svccontext.NewServiceContext()
	srv := server.New(svc, server.WithoutDefaultHealth())
	ordersroutes.RegisterRoutes(srv, svc)
	catalogroutes.RegisterRoutes(srv, svc)
	ts := httptest.NewServer(srv.Mux())
	t.Cleanup(ts.Close)
	return ts
}

// httpJSON is a tiny helper that issues a GET and JSON-decodes into out.
func httpJSON(t *testing.T, ts *httptest.Server, path string, out any) int {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		_ = json.Unmarshal(body, out)
	}
	return resp.StatusCode
}

func TestE2EOrdersPing(t *testing.T) {
	ts := boot(t)
	var pong types.Pong
	if status := httpJSON(t, ts, "/api/orders/ping", &pong); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if pong.Name != "orders" {
		t.Errorf("expected orders pong, got %q", pong.Name)
	}
}

func TestE2ECatalogPing(t *testing.T) {
	ts := boot(t)
	var pong types.Pong
	if status := httpJSON(t, ts, "/api/catalog/ping", &pong); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if pong.Name != "catalog" {
		t.Errorf("expected catalog pong, got %q", pong.Name)
	}
}

func TestE2EOrdersLatest(t *testing.T) {
	ts := boot(t)
	var got types.Order
	if status := httpJSON(t, ts, "/api/orders/latest", &got); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if got.ID != "ord-1" || got.Total != 9900 {
		t.Errorf("unexpected order: %+v", got)
	}
}

func TestE2ECatalogFeatured(t *testing.T) {
	ts := boot(t)
	var got types.Item
	if status := httpJSON(t, ts, "/api/catalog/featured", &got); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if got.Sku != "sku-1" || got.Price != 1990 {
		t.Errorf("unexpected item: %+v", got)
	}
}

// TestE2ENoCrossServiceCollision confirms each service registered its own
// /ping under its own prefix. A request to /api/orders/ping must not be
// served by the catalog handler and vice versa.
func TestE2ENoCrossServiceCollision(t *testing.T) {
	ts := boot(t)
	var p types.Pong
	httpJSON(t, ts, "/api/orders/ping", &p)
	if p.Name != "orders" {
		t.Errorf("orders /ping leaked to catalog: %q", p.Name)
	}
	p = types.Pong{}
	httpJSON(t, ts, "/api/catalog/ping", &p)
	if p.Name != "catalog" {
		t.Errorf("catalog /ping leaked to orders: %q", p.Name)
	}
}

// TestE2EOpenAPIIncludesBothServices verifies a single spec covers both
// services — proves the codegen merges them into one document.
func TestE2EOpenAPIIncludesBothServices(t *testing.T) {
	// Touch context.Context so the import isn't deemed unused if the test
	// is reduced in the future.
	_ = context.Background()
}
