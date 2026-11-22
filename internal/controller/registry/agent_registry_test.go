package registry

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	agentpb "github.com/agomez/pulsar/gen/proto/agent"
)

// heartbeat mimics the field updates upsert performs under r.mu, without the
// etcd lease refresh (which needs a live etcd client).
func heartbeat(r *AgentRegistry, key string, used int32) {
	r.mu.Lock()
	rec := r.agents[key]
	rec.LastSeen = time.Now()
	rec.Resources = &agentpb.ResourceUsage{VcpusTotal: 64, VcpusUsed: used}
	r.mu.Unlock()
}

func newTestRegistry() (*AgentRegistry, string) {
	r := NewAgentRegistry(nil, zap.NewNop())
	key := registryKey("compute", "node-1")
	r.agents[key] = &AgentRecord{
		AgentID:   "node-1",
		Pillar:    "compute",
		Resources: &agentpb.ResourceUsage{VcpusTotal: 64},
	}
	return r, key
}

func TestListReturnsSnapshots(t *testing.T) {
	r, key := newTestRegistry()

	before := r.ListByPillar("compute")[0]
	heartbeat(r, key, 10)

	if got := before.Resources.GetVcpusUsed(); got != 0 {
		t.Fatalf("earlier snapshot changed after heartbeat: vcpus_used=%d", got)
	}
	if got := r.ListAll()[0].Resources.GetVcpusUsed(); got != 10 {
		t.Fatalf("new snapshot missing heartbeat update: vcpus_used=%d", got)
	}

	// Mutating a returned record must not affect the registry.
	before.Pillar = "mutated"
	if got := r.ListByPillar("compute"); len(got) != 1 {
		t.Fatalf("caller mutation leaked into registry: %d compute agents", len(got))
	}
}

// Run with -race: readers use returned records without the lock while
// heartbeats update the live record.
func TestListConcurrentWithHeartbeats(t *testing.T) {
	r, key := newTestRegistry()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := int32(0); i < 500; i++ {
			heartbeat(r, key, i%64)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			for _, a := range r.ListByPillar("compute") {
				_ = a.LastSeen
				_ = a.Resources.GetVcpusUsed()
			}
			for _, a := range r.ListAll() {
				_ = a.LastSeen
			}
		}
	}()
	wg.Wait()
}
