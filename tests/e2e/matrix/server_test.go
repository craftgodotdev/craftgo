package matrix

import (
	"encoding/json"
	"net/http"
	"testing"

	adminapitypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/adminapi"
	runtimetypes "github.com/craftgodotdev/craftgo/tests/e2e/matrix/internal/types/runtime"
)

// ---- multi-service: two services coexist on one server ----

func TestServer_OrdersPing(t *testing.T) {
	ts, _ := boot(t)
	var p runtimetypes.RtPong
	if s := getJSON(t, ts, "/api/orders/ping", &p); s != http.StatusOK {
		t.Fatalf("status %d", s)
	}
	if p.Name != "orders" {
		t.Errorf("got %q", p.Name)
	}
}

func TestServer_CatalogPing(t *testing.T) {
	ts, _ := boot(t)
	var p runtimetypes.RtPong
	if s := getJSON(t, ts, "/api/catalog/ping", &p); s != http.StatusOK {
		t.Fatalf("status %d", s)
	}
	if p.Name != "catalog" {
		t.Errorf("got %q", p.Name)
	}
}

func TestServer_OrdersLatestSeeded(t *testing.T) {
	ts, _ := boot(t)
	var o runtimetypes.RtOrder
	if s := getJSON(t, ts, "/api/orders/latest", &o); s != http.StatusOK {
		t.Fatalf("status %d", s)
	}
	if o.ID != "ord-1" || o.Total != 9900 {
		t.Errorf("unexpected order: %+v", o)
	}
}

func TestServer_CatalogFeaturedSeeded(t *testing.T) {
	ts, _ := boot(t)
	var i runtimetypes.RtItem
	if s := getJSON(t, ts, "/api/catalog/featured", &i); s != http.StatusOK {
		t.Fatalf("status %d", s)
	}
	if i.Sku != "sku-1" || i.Price != 1990 {
		t.Errorf("unexpected item: %+v", i)
	}
}

func TestServer_NoCrossServiceCollision(t *testing.T) {
	ts, _ := boot(t)
	var p runtimetypes.RtPong
	getJSON(t, ts, "/api/orders/ping", &p)
	if p.Name != "orders" {
		t.Errorf("orders /ping leaked: %q", p.Name)
	}
	p = runtimetypes.RtPong{}
	getJSON(t, ts, "/api/catalog/ping", &p)
	if p.Name != "catalog" {
		t.Errorf("catalog /ping leaked: %q", p.Name)
	}
}

// ---- account-user service: CRUD + stateful store + validation ----

func TestServer_AccountCreateAndGet(t *testing.T) {
	ts, _ := boot(t)
	body := map[string]any{"name": "Ada", "tags": []string{"x"}, "meta": map[string]string{"k": "v"}}
	st, raw := reqJSON(t, ts, http.MethodPost, "/api/account-users/users", body)
	if st != http.StatusCreated {
		t.Fatalf("create status %d: %s", st, raw)
	}
	var created runtimeUser
	_ = json.Unmarshal(raw, &created)
	if created.ID == "" || created.Name != "Ada" {
		t.Fatalf("bad created: %+v", created)
	}
	var got runtimeUser
	if s := getJSON(t, ts, "/api/account-users/users/"+created.ID, &got); s != http.StatusOK {
		t.Fatalf("get status %d", s)
	}
	if got.Name != "Ada" {
		t.Errorf("got %+v", got)
	}
}

func TestServer_AccountGetMissing404(t *testing.T) {
	ts, _ := boot(t)
	if s := getJSON(t, ts, "/api/account-users/users/nope", nil); s != http.StatusNotFound {
		t.Fatalf("status %d", s)
	}
}

func TestServer_AccountPing204(t *testing.T) {
	ts, _ := boot(t)
	if s := getJSON(t, ts, "/api/account-users/ping", nil); s != http.StatusNoContent {
		t.Fatalf("status %d", s)
	}
}

func TestServer_AccountDeleteScoped(t *testing.T) {
	ts, svc := boot(t)
	mk := func(name string) string {
		_, raw := reqJSON(t, ts, http.MethodPost, "/api/account-users/users", map[string]any{"name": name, "tags": []string{}, "meta": map[string]string{}})
		var u runtimeUser
		_ = json.Unmarshal(raw, &u)
		return u.ID
	}
	id1, id2 := mk("a"), mk("b")
	if s, _ := reqJSON(t, ts, http.MethodDelete, "/api/account-users/users/"+id1, nil); s != http.StatusOK {
		t.Fatalf("delete status %d", s)
	}
	svc.Lock()
	_, has1 := svc.Users[id1]
	_, has2 := svc.Users[id2]
	svc.Unlock()
	if has1 || !has2 {
		t.Errorf("delete must be path-scoped: has1=%v has2=%v", has1, has2)
	}
}

func TestServer_AccountUpdatePathBind(t *testing.T) {
	ts, _ := boot(t)
	_, raw := reqJSON(t, ts, http.MethodPost, "/api/account-users/users", map[string]any{"name": "old", "tags": []string{}, "meta": map[string]string{}})
	var u runtimeUser
	_ = json.Unmarshal(raw, &u)
	st, body := reqJSON(t, ts, http.MethodPut, "/api/account-users/users/"+u.ID, map[string]any{"name": "new", "tags": []string{}, "meta": map[string]string{}})
	if st != http.StatusOK {
		t.Fatalf("update status %d: %s", st, body)
	}
	var got runtimeUser
	_ = json.Unmarshal(body, &got)
	if got.ID != u.ID || got.Name != "new" {
		t.Errorf("got %+v", got)
	}
}

func TestServer_AccountValidationRejects(t *testing.T) {
	ts, _ := boot(t)
	// empty name violates @length(1,50)
	if s, _ := reqJSON(t, ts, http.MethodPost, "/api/account-users/users", map[string]any{"name": "", "tags": []string{}, "meta": map[string]string{}}); s != http.StatusBadRequest {
		t.Errorf("empty name should 400, got %d", s)
	}
	// malformed JSON
	if s, _ := reqJSON(t, ts, http.MethodPost, "/api/account-users/users", "not-json"); s == http.StatusOK {
		t.Errorf("bad JSON should not 2xx, got %d", s)
	}
}

func TestServer_UnknownRoute404(t *testing.T) {
	ts, _ := boot(t)
	if s := getJSON(t, ts, "/api/nope/nothing", nil); s != http.StatusNotFound {
		t.Errorf("status %d", s)
	}
}

type runtimeUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TestServer_AdminApiNestedGroups exercises one service (AdminApi) whose
// methods are split across three NESTED @group folders — admin/v1 (primary),
// admin/v2 and admin/v3 (extend blocks). Each group emits its own routes file
// (routes/admin/v1, /v2, /v3) registered separately in boot(), mirroring the
// per-group transport + service split. @group never touches the URL: the paths
// come from @prefix("/adminapi") + each method path, independent of the on-disk
// group folder.
func TestServer_AdminApiNestedGroups(t *testing.T) {
	ts, _ := boot(t)
	for _, c := range []struct {
		path, want string
	}{
		{"/api/adminapi/v1/ping", "v1"},
		{"/api/adminapi/v2/ping", "v2"},
		{"/api/adminapi/v3/ping", "v3"},
	} {
		var r adminapitypes.VerResp
		if s := getJSON(t, ts, c.path, &r); s != http.StatusOK {
			t.Fatalf("%s status %d", c.path, s)
		}
		if r.Version != c.want || !r.Ok {
			t.Errorf("%s: got {Version:%q Ok:%v}, want {Version:%q Ok:true}", c.path, r.Version, r.Ok, c.want)
		}
	}
}
