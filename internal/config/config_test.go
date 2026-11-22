package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControllerValidate(t *testing.T) {
	cases := []struct {
		name    string
		secret  string
		wantErr string
	}{
		{"empty", "", "not set"},
		{"docker placeholder", "pulsar-dev-change-in-production", "placeholder"},
		{"helm placeholder", "pulsar-change-in-production", "placeholder"},
		{"placeholder case-insensitive", "CHANGE-ME", "placeholder"},
		{"too short", "abc123", "at least"},
		{"ok", strings.Repeat("a", MinJWTSecretLen), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ControllerConfig{JWT: JWTConfig{Secret: tc.secret}}
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// Secrets omitted from the config file must still be read from env vars.
func TestLoadBindsSecretEnvVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pulsar.yaml")
	if err := os.WriteFile(path, []byte("controller:\n  jwt:\n    expiry: 1h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSAR_CONTROLLER_JWT_SECRET", "from-env-secret")
	t.Setenv("PULSAR_CONTROLLER_POSTGRES_DSN", "postgres://u:p@h/db")
	t.Setenv("PULSAR_CONTROLLER_OIDC_BASE_URL", "https://pulsar.example.test")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Controller.JWT.Secret; got != "from-env-secret" {
		t.Errorf("jwt.secret = %q, want from env", got)
	}
	if got := cfg.Controller.Postgres.DSN; got != "postgres://u:p@h/db" {
		t.Errorf("postgres.dsn = %q, want from env", got)
	}
	if got := cfg.Controller.OIDCBaseURL; got != "https://pulsar.example.test" {
		t.Errorf("oidc_base_url = %q, want from env", got)
	}
}

func TestOIDCAllowedNetworks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pulsar.yaml")
	if err := os.WriteFile(path, []byte("controller: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSAR_CONTROLLER_OIDC_ALLOWED_NETWORKS", "10.11.0.0/16,192.168.1.5")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Controller.OIDCAllowedNetworks
	if len(got) != 2 || got[0] != "10.11.0.0/16" || got[1] != "192.168.1.5" {
		t.Fatalf("oidc_allowed_networks from env = %q", got)
	}

	c := ControllerConfig{JWT: JWTConfig{Secret: strings.Repeat("a", MinJWTSecretLen)}}
	c.OIDCAllowedNetworks = []string{"10.0.0.0/8", "192.168.1.5"}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid networks rejected: %v", err)
	}
	c.OIDCAllowedNetworks = []string{"10.0.0.0/33"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "oidc_allowed_networks") {
		t.Fatalf("invalid CIDR accepted: %v", err)
	}

	// docker-compose passes an empty value when the variable is unset.
	t.Setenv("PULSAR_CONTROLLER_OIDC_ALLOWED_NETWORKS", "")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Controller.JWT.Secret = strings.Repeat("a", MinJWTSecretLen)
	if err := cfg.Controller.Validate(); err != nil {
		t.Fatalf("empty oidc_allowed_networks rejected: %v", err)
	}
}

func TestQuotaDefaultsFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulsar.yaml")
	yaml := "controller:\n  quota_defaults:\n    vcpus: 64\n    Instances: -1\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Controller.QuotaDefaults
	if got["vcpus"] != 64 || got["instances"] != -1 || len(got) != 2 {
		t.Fatalf("quota_defaults = %v", got)
	}
}

func TestGRPCTLSConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulsar.yaml")
	if err := os.WriteFile(path, []byte("controller: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSAR_CONTROLLER_GRPC_TLS_CERT", "/pki/controller.crt")
	t.Setenv("PULSAR_AGENT_TLS_SERVER_NAME", "pulsar.internal")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Controller.GRPCTLS.Cert != "/pki/controller.crt" || cfg.Agent.TLS.ServerName != "pulsar.internal" {
		t.Fatalf("env not bound: %+v / %+v", cfg.Controller.GRPCTLS, cfg.Agent.TLS)
	}
	// Half-configured TLS is rejected at start-up rather than silently ignored.
	c := ControllerConfig{JWT: JWTConfig{Secret: strings.Repeat("a", MinJWTSecretLen)}}
	c.GRPCTLS = TLSConfig{Cert: "/pki/controller.crt"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "grpc_tls") {
		t.Fatalf("partial grpc_tls accepted: %v", err)
	}
}
