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

// Through the real router: project readers are read-only on project
// resources, but keypairs (user-scoped) stay manageable.
func TestReaderRoleThroughRouter(t *testing.T) {
	db := testutil.Postgres(t)
	store := testutil.Etcd(t)
	ctx := context.Background()

	cfg := &config.Config{}
	cfg.Controller.JWT.Secret = "router-test-secret-0123456789abcdef01234"
	cfg.Controller.JWT.Expiry = time.Hour
	cfg.Controller.ImageStoreDir = t.TempDir()
	srv := httptest.NewServer(newRouter(cfg, zap.NewNop(), store, db, registry.NewAgentRegistry(store, zap.NewNop())))
	defer srv.Close()

	org, proj, user := id.New(), id.New(), id.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $2)`, []any{org, "o-" + org}},
		{`INSERT INTO projects (id, name, org_id) VALUES ($1, 'p', $2)`, []any{proj, org}},
		{`INSERT INTO users (id, org_id, email, password_hash, project_id) VALUES ($1, $2, $3, '', $4)`,
			[]any{user, org, "r-" + user + "@o.test", proj}},
		{`INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, 'reader')`, []any{proj, user}},
	} {
		if _, err := db.Pool().Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": user, "org_id": org, "project_id": proj, "role": "member", "org_role": "member",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(cfg.Controller.JWT.Secret))

	call := func(method, path, body string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	if got, body := call("GET", "/v1/network/networks", ""); got != 200 {
		t.Fatalf("reader list networks: %d %s", got, body)
	}
	if got, body := call("POST", "/v1/network/networks", `{"name":"n"}`); got != 403 || !strings.Contains(body, "PROJECT_READ_ONLY") {
		t.Fatalf("reader create network: %d %s, want 403 PROJECT_READ_ONLY", got, body)
	}
	if got, body := call("POST", "/v1/compute/instances", `{}`); got != 403 {
		t.Fatalf("reader create instance: %d %s, want 403", got, body)
	}

	got, body := call("POST", "/v1/compute/keypairs", `{"name":"laptop"}`)
	if got != http.StatusCreated {
		t.Fatalf("reader create own keypair: %d %s, want 201", got, body)
	}
	var env struct {
		Data struct {
			KeyPair struct {
				ID string `json:"id"`
			} `json:"keypair"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || env.Data.KeyPair.ID == "" {
		t.Fatalf("keypair id missing from %s", body)
	}
	if got, body := call("DELETE", "/v1/compute/keypairs/"+env.Data.KeyPair.ID, ""); got != http.StatusNoContent {
		t.Fatalf("reader delete own keypair: %d %s", got, body)
	}
}
