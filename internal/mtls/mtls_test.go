package mtls

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(b)
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestIssueCertificates(t *testing.T) {
	dir := t.TempDir()
	if err := InitCA(dir, "test CA", 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := InitCA(dir, "again", time.Hour); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second InitCA: %v", err)
	}
	if err := Issue(dir, dir, "controller", ServerAndClient, []string{"pulsar.local"}, []net.IP{net.ParseIP("10.0.0.5")}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := Issue(dir, dir, "compute-1", ClientOnly, nil, nil, time.Hour); err != nil {
		t.Fatal(err)
	}

	ca := readCert(t, filepath.Join(dir, "ca.crt"))
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if !ca.IsCA || !ca.MaxPathLenZero {
		t.Error("CA must be a CA that can't sign intermediates")
	}

	ctrl := readCert(t, filepath.Join(dir, "controller.crt"))
	if _, err := ctrl.Verify(x509.VerifyOptions{Roots: pool, DNSName: "pulsar.local", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Errorf("controller cert as server: %v", err)
	}
	if len(ctrl.IPAddresses) != 1 || !ctrl.IPAddresses[0].Equal(net.ParseIP("10.0.0.5")) {
		t.Errorf("controller IP SANs: %v", ctrl.IPAddresses)
	}

	agent := readCert(t, filepath.Join(dir, "compute-1.crt"))
	if agent.Subject.CommonName != "compute-1" {
		t.Errorf("agent CN = %q", agent.Subject.CommonName)
	}
	if _, err := agent.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Errorf("agent cert as client: %v", err)
	}
	if slices.Contains(agent.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		t.Error("agent cert must not be usable as a server cert")
	}
	for _, key := range []string{"ca.key", "controller.key", "compute-1.key"} {
		fi, _ := os.Stat(filepath.Join(dir, key))
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", key, fi.Mode().Perm())
		}
	}
}

func TestFilesValidate(t *testing.T) {
	dir := t.TempDir()
	_ = InitCA(dir, "ca", time.Hour)
	_ = Issue(dir, dir, "a", ClientOnly, nil, nil, time.Hour)
	p := func(n string) string { return filepath.Join(dir, n) }

	if err := (Files{}).Validate(); err != nil {
		t.Errorf("empty (TLS off) must be valid: %v", err)
	}
	if err := (Files{CACert: p("ca.crt"), Cert: p("a.crt"), Key: p("a.key")}).Validate(); err != nil {
		t.Errorf("complete: %v", err)
	}
	for name, f := range map[string]Files{
		"missing key":   {CACert: p("ca.crt"), Cert: p("a.crt")},
		"missing CA":    {Cert: p("a.crt"), Key: p("a.key")},
		"wrong key":     {CACert: p("ca.crt"), Cert: p("a.crt"), Key: p("ca.key")},
		"CA isn't PEM":  {CACert: p("a.key"), Cert: p("a.crt"), Key: p("a.key")},
		"no such files": {CACert: p("x"), Cert: p("y"), Key: p("z")},
	} {
		if err := f.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
