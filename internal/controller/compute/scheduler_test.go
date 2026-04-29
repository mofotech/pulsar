package compute

import (
	"testing"
	"time"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
	"github.com/agomez/pulsar/internal/controller/registry"
)

func makeAgent(id, pillar string, hypervisors []string, vcpusTotal, vcpusUsed int32) *registry.AgentRecord {
	return &registry.AgentRecord{
		AgentID: id,
		Pillar:  pillar,
		LastSeen: time.Now(),
		Capabilities: agentpb.AgentCapabilities{
			HypervisorTypes: hypervisors,
		},
		Resources: agentpb.ResourceUsage{
			VcpusTotal: vcpusTotal,
			VcpusUsed:  vcpusUsed,
		},
	}
}

func TestPickAgent_SelectsLeastLoaded(t *testing.T) {
	agents := []*registry.AgentRecord{
		makeAgent("node-a", "compute", []string{"kvm"}, 16, 14), // 87.5% used
		makeAgent("node-b", "compute", []string{"kvm"}, 16, 4),  // 25% used  ← should win
		makeAgent("node-c", "compute", []string{"kvm"}, 16, 10), // 62.5% used
	}

	picked := pickAgent(agents, "kvm")
	if picked == nil {
		t.Fatal("expected an agent to be picked, got nil")
	}
	if picked.AgentID != "node-b" {
		t.Fatalf("expected node-b (least loaded), got %s", picked.AgentID)
	}
}

func TestPickAgent_FiltersHypervisorType(t *testing.T) {
	agents := []*registry.AgentRecord{
		makeAgent("node-a", "compute", []string{"kvm"}, 16, 1),
		makeAgent("node-b", "compute", []string{"lxd"}, 16, 0),
	}

	picked := pickAgent(agents, "lxd")
	if picked == nil {
		t.Fatal("expected an agent to be picked")
	}
	if picked.AgentID != "node-b" {
		t.Fatalf("expected node-b (supports lxd), got %s", picked.AgentID)
	}
}

func TestPickAgent_NoMatchReturnsNil(t *testing.T) {
	agents := []*registry.AgentRecord{
		makeAgent("node-a", "compute", []string{"kvm"}, 16, 1),
	}

	picked := pickAgent(agents, "containerd")
	if picked != nil {
		t.Fatalf("expected nil for unsupported hypervisor type, got %s", picked.AgentID)
	}
}

func TestPickAgent_EmptyListReturnsNil(t *testing.T) {
	if picked := pickAgent(nil, "kvm"); picked != nil {
		t.Fatal("expected nil for empty agent list")
	}
}

func TestPickAgent_ZeroTotalVCPUs(t *testing.T) {
	// Agents that haven't reported resources yet (vcpus_total=0) should still be selectable
	agents := []*registry.AgentRecord{
		makeAgent("node-a", "compute", []string{"kvm"}, 0, 0),
	}
	picked := pickAgent(agents, "kvm")
	if picked == nil {
		t.Fatal("expected agent with zero vcpus_total to be pickable")
	}
}
