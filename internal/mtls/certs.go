package mtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Usage selects the extended key usages of an issued certificate.
type Usage int

const (
	// ServerAndClient: the controller (serves agents; may later dial out).
	ServerAndClient Usage = iota
	// ClientOnly: an agent.
	ClientOnly
)

// InitCA writes ca.crt and ca.key into dir. It refuses to overwrite an
// existing CA, since every issued certificate depends on it.
func InitCA(dir, commonName string, validity time.Duration) error {
	crtPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	for _, p := range []string{crtPath, keyPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s already exists; refusing to overwrite the CA", p)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"Pulsar"}},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writePair(crtPath, keyPath, der, key)
}

// Issue signs a certificate for name with the CA in caDir and writes
// <name>.crt / <name>.key into outDir. Agents use their node_id as name.
func Issue(caDir, outDir, name string, usage Usage, dnsNames []string, ips []net.IP, validity time.Duration) error {
	if name == "" {
		return errors.New("name is required")
	}
	ca, err := tls.LoadX509KeyPair(filepath.Join(caDir, "ca.crt"), filepath.Join(caDir, "ca.key"))
	if err != nil {
		return fmt.Errorf("load CA from %s: %w", caDir, err)
	}
	caCert, err := x509.ParseCertificate(ca.Certificate[0])
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	ext := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	if usage == ServerAndClient {
		ext = append(ext, x509.ExtKeyUsageServerAuth)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: name, Organization: []string{"Pulsar"}},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  ext,
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, ca.PrivateKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return err
	}
	return writePair(filepath.Join(outDir, name+".crt"), filepath.Join(outDir, name+".key"), der, key)
}

func writePair(crtPath, keyPath string, der []byte, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(crtPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	return os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}
