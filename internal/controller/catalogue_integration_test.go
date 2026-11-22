package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/config"
	"github.com/agomez/pulsar/internal/controller/registry"
	"github.com/agomez/pulsar/internal/testutil"
	"github.com/agomez/pulsar/pkg/id"
)

type apiClient struct {
	t       *testing.T
	srv     *httptest.Server
	token   string
	project string
}

func (c apiClient) call(method, path, body string) (int, string) {
	c.t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, c.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	if c.project != "" {
		req.Header.Set("X-Project-Id", c.project)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func dataID(t *testing.T, body string) string {
	t.Helper()
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || env.Data.ID == "" {
		t.Fatalf("no id in %s", body)
	}
	return env.Data.ID
}

// Shared catalogue resources (flavors, volume types, images) must not be
// changeable by ordinary tenants, and private images stay inside their
// project — including for booting and volume creation.
func TestCatalogueAuthorization(t *testing.T) {
	db := testutil.Postgres(t)
	store := testutil.Etcd(t)
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.Controller.JWT.Secret = "catalogue-test-secret-0123456789abcdef"
	cfg.Controller.ImageStoreDir = t.TempDir()
	srv := httptest.NewServer(newRouter(cfg, zap.NewNop(), store, db, registry.NewAgentRegistry(store, zap.NewNop())))
	defer srv.Close()

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Pool().Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	newTenant := func(label string) apiClient {
		org, proj, user := id.New(), id.New(), id.New()
		exec(`INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $2)`, org, label+"-"+org)
		exec(`INSERT INTO projects (id, name, org_id) VALUES ($1, $2, $3)`, proj, label, org)
		exec(`INSERT INTO users (id, org_id, email, password_hash, project_id) VALUES ($1, $2, $3, '', $4)`,
			user, org, label+"-"+user+"@t.test", proj)
		exec(`INSERT INTO project_members (project_id, user_id) VALUES ($1, $2)`, proj, user)
		tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": user, "org_id": org, "project_id": proj, "role": "member", "org_role": "member",
			"exp": time.Now().Add(time.Hour).Unix(),
		}).SignedString([]byte(cfg.Controller.JWT.Secret))
		return apiClient{t: t, srv: srv, token: tok, project: proj}
	}
	a, b := newTenant("a"), newTenant("b")
	adminTok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": id.New(), "org_id": testutil.DefaultOrgID, "role": "admin", "org_role": "platform_admin",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(cfg.Controller.JWT.Secret))
	admin := apiClient{t: t, srv: srv, token: adminTok, project: a.project}

	t.Run("flavors and volume types are platform-admin only", func(t *testing.T) {
		flavor := `{"name":"f-` + id.New() + `","vcpus":1,"ram_mb":512,"disk_gb":1}`
		if got, _ := a.call("POST", "/v1/compute/flavors", flavor); got != http.StatusForbidden {
			t.Errorf("member create flavor: %d, want 403", got)
		}
		got, body := admin.call("POST", "/v1/compute/flavors", flavor)
		if got != http.StatusCreated {
			t.Fatalf("admin create flavor: %d %s", got, body)
		}
		fid := dataID(t, body)
		if got, _ := a.call("DELETE", "/v1/compute/flavors/"+fid, ""); got != http.StatusForbidden {
			t.Errorf("member delete flavor: %d, want 403", got)
		}
		if got, _ := a.call("GET", "/v1/compute/flavors/"+fid, ""); got != http.StatusOK {
			t.Errorf("member read flavor: %d, want 200", got)
		}
		if got, _ := admin.call("DELETE", "/v1/compute/flavors/"+fid, ""); got != http.StatusNoContent {
			t.Errorf("admin delete flavor: %d", got)
		}
		if got, _ := a.call("POST", "/v1/storage/volume-types", `{"name":"fast","backend":"lvm"}`); got != http.StatusForbidden {
			t.Errorf("member create volume type: %d, want 403", got)
		}
	})

	t.Run("private images stay in their project", func(t *testing.T) {
		got, body := a.call("POST", "/v1/images", `{"name":"ubuntu-22.04","url":"https://img.example/u.qcow2"}`)
		if got != http.StatusCreated || !strings.Contains(body, `"visibility":"private"`) || !strings.Contains(body, a.project) {
			t.Fatalf("member register image: %d %s", got, body)
		}
		priv := dataID(t, body)

		_, list := b.call("GET", "/v1/images", "")
		if strings.Contains(list, priv) {
			t.Error("other project can list a private image")
		}
		if got, _ := b.call("GET", "/v1/images/"+priv, ""); got != http.StatusNotFound {
			t.Errorf("other project get: %d, want 404", got)
		}
		if got, _ := b.call("DELETE", "/v1/images/"+priv, ""); got != http.StatusNotFound {
			t.Errorf("other project delete: %d, want 404", got)
		}
		// Can't boot it or build a volume from it either.
		if got, body := b.call("POST", "/v1/compute/instances", `{"name":"x","flavor_id":"nope","image_id":"`+priv+`"}`); got == http.StatusAccepted {
			t.Errorf("other project booted a private image: %s", body)
		}
		got, body = b.call("POST", "/v1/storage/volumes", `{"name":"v","size_gb":1,"image_id":"`+priv+`"}`)
		if got == http.StatusCreated || got == http.StatusAccepted || !strings.Contains(body, "not found") {
			t.Errorf("other project built a volume from a private image: %d %s", got, body)
		}
		if got, _ := a.call("GET", "/v1/images/"+priv, ""); got != http.StatusOK {
			t.Errorf("owner get: %d", got)
		}
		if got, _ := a.call("DELETE", "/v1/images/"+priv, ""); got != http.StatusNoContent {
			t.Errorf("owner delete: %d", got)
		}
	})

	t.Run("only platform admins publish public images", func(t *testing.T) {
		pub := `{"name":"debian-12","url":"https://img.example/d.qcow2","visibility":"public"}`
		if got, _ := a.call("POST", "/v1/images", pub); got != http.StatusForbidden {
			t.Errorf("member publish public: %d, want 403", got)
		}
		got, body := admin.call("POST", "/v1/images", pub)
		if got != http.StatusCreated {
			t.Fatalf("admin publish: %d %s", got, body)
		}
		pubID := dataID(t, body)
		if got, _ := b.call("GET", "/v1/images/"+pubID, ""); got != http.StatusOK {
			t.Errorf("tenant can't see public image: %d", got)
		}
		if got, _ := b.call("DELETE", "/v1/images/"+pubID, ""); got != http.StatusForbidden {
			t.Errorf("tenant deleted public image: %d, want 403", got)
		}
		if got, _ := a.call("POST", "/v1/images", `{"name":"x","url":"u","visibility":"world"}`); got != http.StatusBadRequest {
			t.Errorf("bad visibility: %d, want 400", got)
		}
		admin.call("DELETE", "/v1/images/"+pubID, "")
	})

	t.Run("pre-existing images (no owner) remain public", func(t *testing.T) {
		legacy := id.New()
		raw, _ := json.Marshal(map[string]any{"id": legacy, "name": "old", "status": "active", "format": "qcow2"})
		if err := store.Put(ctx, "/pulsar/images/"+legacy, string(raw)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Delete(context.Background(), "/pulsar/images/"+legacy) })
		if got, _ := b.call("GET", "/v1/images/"+legacy, ""); got != http.StatusOK {
			t.Errorf("legacy image hidden: %d", got)
		}
		if got, _ := b.call("DELETE", "/v1/images/"+legacy, ""); got != http.StatusForbidden {
			t.Errorf("tenant deleted legacy image: %d, want 403", got)
		}
	})
}
