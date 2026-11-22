package compute

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/controller/quota"
	"github.com/agomez/pulsar/internal/controller/registry"
	"github.com/agomez/pulsar/internal/testutil"
	"github.com/agomez/pulsar/pkg/id"
)

type staticLimits quota.Limits

func (l staticLimits) Limits(context.Context, string) (quota.Limits, error) {
	return quota.Limits(l), nil
}

// End to end through the real service: usage is computed from etcd and
// CreateInstance / ResizeInstance reject requests over the project's limits.
func TestInstanceQuotas(t *testing.T) {
	store := testutil.Etcd(t)
	ctx := context.Background()
	s := NewService(store, registry.NewAgentRegistry(store, zap.NewNop()), nil, nil, zap.NewNop())
	e := quota.NewEnforcer(staticLimits{quota.Instances: 2, quota.VCPUs: 6, quota.RAMMB: quota.Unlimited}, store, zap.NewNop())
	e.RegisterUsage(s.QuotaUsage)
	s.SetQuotaEnforcer(e)

	project := "quota-test-" + id.New()
	small := &Flavor{ID: "q-small-" + id.New(), Name: "small", VCPUs: 2, RamMB: 1024}
	big := &Flavor{ID: "q-big-" + id.New(), Name: "big", VCPUs: 8, RamMB: 4096}
	imageID := "q-img-" + id.New()
	for _, f := range []*Flavor{small, big} {
		if err := s.CreateFlavor(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	img, _ := json.Marshal(map[string]any{"id": imageID, "name": "img"})
	if err := store.Put(ctx, "/pulsar/images/"+imageID, string(img)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		vals, _ := store.GetPrefix(ctx, instanceKeyPrefix+project+"/")
		for k := range vals {
			_ = store.Delete(ctx, k)
		}
		_ = store.Delete(ctx, flavorKeyPrefix+small.ID)
		_ = store.Delete(ctx, flavorKeyPrefix+big.ID)
		_ = store.Delete(ctx, "/pulsar/images/"+imageID)
	})

	launch := func(f *Flavor) error {
		_, err := s.CreateInstance(ctx, project, "user", CreateInstanceRequest{Name: "vm", FlavorID: f.ID, ImageID: imageID})
		return err
	}
	if err := launch(big); !quota.IsExceeded(err) {
		t.Fatalf("8 vCPUs against a limit of 6: got %v, want quota exceeded", err)
	}
	if err := launch(small); err != nil {
		t.Fatalf("first small instance: %v", err)
	}
	if err := launch(small); err != nil {
		t.Fatalf("second small instance: %v", err)
	}
	err := launch(small)
	var ex *quota.ExceededError
	if !quota.IsExceeded(err) {
		t.Fatalf("third instance: got %v, want quota exceeded", err)
	}
	if errors.As(err, &ex); ex.Resource != quota.Instances || ex.Used != 2 || ex.Limit != 2 {
		t.Fatalf("unexpected detail %+v", ex)
	}

	u, err := s.QuotaUsage(ctx, project)
	if err != nil || u[quota.Instances] != 2 || u[quota.VCPUs] != 4 || u[quota.RAMMB] != 2048 {
		t.Fatalf("usage %v err %v", u, err)
	}

	// Resize up past the vCPU limit is rejected before anything is dispatched.
	running := &Instance{ID: id.New(), ProjectID: "resize-" + project, FlavorID: small.ID,
		Status: StateActive, NodeID: "node-1", CreatedAt: time.Now()}
	if err := s.saveInstance(ctx, running); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Delete(context.Background(), instanceKeyPrefix+running.ProjectID+"/"+running.ID) })
	if _, err := s.ResizeInstance(ctx, running.ProjectID, running.ID, ResizeInstanceRequest{FlavorID: big.ID}); !quota.IsExceeded(err) {
		t.Fatalf("resize 2→8 vCPUs with limit 6: got %v, want quota exceeded", err)
	}
}
