// Package mtls secures the controller ↔ agent gRPC channel with mutual TLS.
//
// The controller presents a server certificate and requires every agent to
// present a client certificate signed by the same CA. An agent's identity is
// its certificate's Common Name, which must equal the agent_id it reports, so
// one leaked agent certificate can't impersonate other nodes.
package mtls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// Files are PEM paths for one side of the channel.
type Files struct {
	CACert, Cert, Key string
	// ServerName overrides the name an agent expects in the controller's
	// certificate (default: the host part of the controller address).
	ServerName string
}

// Enabled reports whether any TLS material is configured.
func (f Files) Enabled() bool { return f.CACert != "" || f.Cert != "" || f.Key != "" }

// Validate requires all three files together, and that they load.
func (f Files) Validate() error {
	if !f.Enabled() {
		return nil
	}
	if f.CACert == "" || f.Cert == "" || f.Key == "" {
		return errors.New("ca_cert, cert and key must all be set to enable mTLS")
	}
	if _, err := tls.LoadX509KeyPair(f.Cert, f.Key); err != nil {
		return fmt.Errorf("load cert/key: %w", err)
	}
	if _, err := loadPool(f.CACert); err != nil {
		return err
	}
	return nil
}

func loadPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ca_cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("ca_cert %s contains no PEM certificates", path)
	}
	return pool, nil
}

// ServerCredentials builds the controller's gRPC credentials: TLS 1.3 and a
// client certificate signed by the CA is mandatory.
func ServerCredentials(f Files) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(f.Cert, f.Key)
	if err != nil {
		return nil, fmt.Errorf("load controller cert/key: %w", err)
	}
	pool, err := loadPool(f.CACert)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

// ClientCredentials builds an agent's gRPC credentials for dialing addr.
func ClientCredentials(f Files, addr string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(f.Cert, f.Key)
	if err != nil {
		return nil, fmt.Errorf("load agent cert/key: %w", err)
	}
	pool, err := loadPool(f.CACert)
	if err != nil {
		return nil, err
	}
	serverName := f.ServerName
	if serverName == "" {
		if host, _, err := net.SplitHostPort(addr); err == nil {
			serverName = host
		} else {
			serverName = addr
		}
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS13,
	}), nil
}

// PeerIdentity returns the Common Name of the verified client certificate on
// a gRPC stream, and false when the connection isn't mutually authenticated.
func PeerIdentity(ctx context.Context) (string, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", false
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.VerifiedChains) == 0 || len(info.State.VerifiedChains[0]) == 0 {
		return "", false
	}
	return info.State.VerifiedChains[0][0].Subject.CommonName, true
}
