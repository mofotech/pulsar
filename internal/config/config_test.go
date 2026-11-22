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
