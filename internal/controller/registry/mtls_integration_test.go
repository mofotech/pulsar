package registry

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
	"github.com/agomez/pulsar/internal/mtls"
	"github.com/agomez/pulsar/internal/testutil"
)

type pki struct{ dir, other string }

func newPKI(t *testing.T) pki {
	t.Helper()
	p := pki{dir: t.TempDir(), other: t.TempDir()}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(mtls.InitCA(p.dir, "test CA", time.Hour))
	must(mtls.Issue(p.dir, p.dir, "controller", mtls.ServerAndClient, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour))
	must(mtls.Issue(p.dir, p.dir, "node-1", mtls.ClientOnly, nil, nil, time.Hour))
	// A second, unrelated CA: its certificates must be refused.
	must(mtls.InitCA(p.other, "rogue CA", time.Hour))
	must(mtls.Issue(p.other, p.other, "node-1", mtls.ClientOnly, nil, nil, time.Hour))
	return p
}

func (p pki) files(dir, name string) mtls.Files {
	return mtls.Files{
		CACert: filepath.Join(p.dir, "ca.crt"), // agents always trust the real CA
		Cert:   filepath.Join(dir, name+".crt"),
		Key:    filepath.Join(dir, name+".key"),
	}
}

// tryHeartbeat opens a stream, claims agentID, and reports whether the
// controller accepted it (registered) or the error it returned.
func tryHeartbeat(t *testing.T, r *AgentRegistry, addr string, creds credentials.TransportCredentials, agentID string) error {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := agentpb.NewAgentGatewayClient(conn).Connect(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&agentpb.AgentMessage{Payload: &agentpb.AgentMessage_Heartbeat{
		Heartbeat: &agentpb.Heartbeat{AgentId: agentID, Pillar: "compute", TimestampUnix: time.Now().Unix()},
	}}); err != nil {
		_, rerr := stream.Recv()
		return rerr
	}
	// Accepted streams stay open; rejected ones end with a status.
	errCh := make(chan error, 1)
	go func() { _, err := stream.Recv(); errCh <- err }()
	deadline := time.After(3 * time.Second)
	for {
		for _, a := range r.ListByPillar("compute") {
			if a.AgentID == agentID {
				return nil
			}
		}
		select {
		case err := <-errCh:
			return err
		case <-deadline:
			t.Fatalf("heartbeat as %s neither registered nor rejected", agentID)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestAgentGatewayMutualTLS(t *testing.T) {
	store := testutil.Etcd(t)
	p := newPKI(t)

	serverCreds, err := mtls.ServerCredentials(mtls.Files{
		CACert: filepath.Join(p.dir, "ca.crt"),
		Cert:   filepath.Join(p.dir, "controller.crt"),
		Key:    filepath.Join(p.dir, "controller.key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	reg := NewAgentRegistry(store, zap.NewNop())
	reg.RequirePeerIdentity(true)
	srv := grpc.NewServer(grpc.Creds(serverCreds))
	reg.RegisterGRPC(srv)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(lis) //nolint:errcheck
	defer srv.Stop()
	addr := lis.Addr().String()

	client := func(dir, name string) credentials.TransportCredentials {
		c, err := mtls.ClientCredentials(p.files(dir, name), addr)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	t.Run("valid certificate, matching agent_id", func(t *testing.T) {
		if err := tryHeartbeat(t, reg, addr, client(p.dir, "node-1"), "node-1"); err != nil {
			t.Fatalf("rejected: %v", err)
		}
	})
	t.Run("valid certificate, someone else's agent_id", func(t *testing.T) {
		err := tryHeartbeat(t, reg, addr, client(p.dir, "node-1"), "node-2")
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("got %v, want PermissionDenied", err)
		}
		for _, a := range reg.ListByPillar("compute") {
			if a.AgentID == "node-2" {
				t.Fatal("impersonated agent was registered")
			}
		}
	})
	t.Run("certificate from another CA", func(t *testing.T) {
		if err := tryHeartbeat(t, reg, addr, client(p.other, "node-1"), "node-1-rogue"); err == nil {
			t.Fatal("accepted a certificate from an untrusted CA")
		}
	})
	t.Run("TLS without a client certificate", func(t *testing.T) {
		// Trusts the server, but presents no certificate of its own.
		c, err := credentials.NewClientTLSFromFile(filepath.Join(p.dir, "ca.crt"), "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if err := tryHeartbeat(t, reg, addr, c, "node-anon"); err == nil {
			t.Fatal("accepted a client without a certificate")
		}
	})
	t.Run("plaintext client", func(t *testing.T) {
		if err := tryHeartbeat(t, reg, addr, insecure.NewCredentials(), "node-plain"); err == nil {
			t.Fatal("accepted a plaintext connection")
		}
	})
}

// Even without TLS a stream can't switch identities after its first heartbeat.
func TestStreamCannotChangeIdentity(t *testing.T) {
	store := testutil.Etcd(t)
	reg := NewAgentRegistry(store, zap.NewNop())
	srv := grpc.NewServer()
	reg.RegisterGRPC(srv)
	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(lis) //nolint:errcheck
	defer srv.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := agentpb.NewAgentGatewayClient(conn).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	send := func(id string) {
		_ = stream.Send(&agentpb.AgentMessage{Payload: &agentpb.AgentMessage_Heartbeat{
			Heartbeat: &agentpb.Heartbeat{AgentId: id, Pillar: "compute"},
		}})
	}
	send("node-a")
	send("node-b")
	if _, err := stream.Recv(); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("got %v, want PermissionDenied", err)
	}
}
