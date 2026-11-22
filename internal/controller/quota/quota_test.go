package quota

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
)

type fakeLimits Limits

func (f fakeLimits) Limits(context.Context, string) (Limits, error) { return Limits(f), nil }

type fakeLocker struct {
	mu     sync.Mutex
	locked []string
}

func (f *fakeLocker) Lock(_ context.Context, key string) (func(), error) {
	f.mu.Lock()
	f.locked = append(f.locked, key)
	return f.mu.Unlock, nil
}

func newTestEnforcer(limits Limits, used Usage) *Enforcer {
	e := NewEnforcer(fakeLimits(limits), &fakeLocker{}, zap.NewNop())
	e.RegisterUsage(func(context.Context, string) (Usage, error) { return used, nil })
	return e
}

func TestReserveWithinLimitCommits(t *testing.T) {
	e := newTestEnforcer(Limits{VCPUs: 20, Instances: 10}, Usage{VCPUs: 16, Instances: 3})
	committed := false
	err := e.Reserve(context.Background(), "p", Usage{VCPUs: 4, Instances: 1}, func() error { committed = true; return nil })
	if err != nil || !committed {
		t.Fatalf("err=%v committed=%v; want commit at exactly the limit", err, committed)
	}
}

func TestReserveOverLimitDoesNotCommit(t *testing.T) {
	e := newTestEnforcer(Limits{VCPUs: 20, Instances: 10}, Usage{VCPUs: 18, Instances: 3})
	committed := false
	err := e.Reserve(context.Background(), "p", Usage{VCPUs: 4, Instances: 1}, func() error { committed = true; return nil })
	var ex *ExceededError
	if !errors.As(err, &ex) || committed {
		t.Fatalf("err=%v committed=%v; want ExceededError and no commit", err, committed)
	}
	if ex.Resource != VCPUs || ex.Requested != 4 || ex.Used != 18 || ex.Limit != 20 {
		t.Fatalf("unexpected error detail: %+v", ex)
	}
	if !strings.Contains(err.Error(), "vcpus: requested 4, 18 of 20 in use") {
		t.Errorf("message = %q", err.Error())
	}
}

func TestUnlimitedAndShrinkingAreNotChecked(t *testing.T) {
	e := newTestEnforcer(Limits{VCPUs: Unlimited, RAMMB: 1024}, Usage{VCPUs: 1000, RAMMB: 4096})
	// Unlimited vCPUs; RAM already over its limit but shrinking (resize down) must be allowed.
	if err := e.Check(context.Background(), "p", Usage{VCPUs: 500, RAMMB: -2048}); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestResourcesWithoutLimitAreUnchecked(t *testing.T) {
	e := newTestEnforcer(Limits{}, Usage{})
	if err := e.Check(context.Background(), "p", Usage{Instances: 999}); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestUsageSumsAllSources(t *testing.T) {
	e := NewEnforcer(fakeLimits{}, &fakeLocker{}, zap.NewNop())
	e.RegisterUsage(func(context.Context, string) (Usage, error) { return Usage{Instances: 2, VCPUs: 4}, nil })
	e.RegisterUsage(func(context.Context, string) (Usage, error) { return Usage{Volumes: 1, VCPUs: 1}, nil })
	u, err := e.Usage(context.Background(), "p")
	if err != nil || u[Instances] != 2 || u[VCPUs] != 5 || u[Volumes] != 1 {
		t.Fatalf("usage=%v err=%v", u, err)
	}
}

func TestUsageErrorFailsClosed(t *testing.T) {
	e := NewEnforcer(fakeLimits{Instances: 10}, &fakeLocker{}, zap.NewNop())
	e.RegisterUsage(func(context.Context, string) (Usage, error) { return nil, errors.New("etcd down") })
	committed := false
	err := e.Reserve(context.Background(), "p", Usage{Instances: 1}, func() error { committed = true; return nil })
	if err == nil || committed {
		t.Fatalf("err=%v committed=%v; a usage failure must block the create", err, committed)
	}
}

func TestReserveLocksPerProject(t *testing.T) {
	l := &fakeLocker{}
	e := NewEnforcer(fakeLimits{}, l, zap.NewNop())
	_ = e.Reserve(context.Background(), "proj-1", Usage{}, func() error { return nil })
	if len(l.locked) != 1 || l.locked[0] != "/pulsar/locks/quota/proj-1" {
		t.Fatalf("locked %v", l.locked)
	}
}

func TestNilEnforcerAllowsEverything(t *testing.T) {
	var e *Enforcer
	committed := false
	if err := e.Reserve(context.Background(), "p", Usage{Instances: 1e6}, func() error { committed = true; return nil }); err != nil || !committed {
		t.Fatalf("err=%v committed=%v", err, committed)
	}
	if err := e.Check(context.Background(), "p", Usage{Instances: 1e6}); err != nil {
		t.Fatal(err)
	}
}

func TestParseDefaults(t *testing.T) {
	got, err := ParseDefaults(map[string]int{"VCPUs": 64, "instances": -1})
	if err != nil {
		t.Fatal(err)
	}
	if got[VCPUs] != 64 || got[Instances] != Unlimited || got[Volumes] != BuiltinDefaults[Volumes] {
		t.Fatalf("got %v", got)
	}
	if _, err := ParseDefaults(map[string]int{"gpus": 1}); err == nil || !strings.Contains(err.Error(), "unknown resource") {
		t.Errorf("unknown resource accepted: %v", err)
	}
	if _, err := ParseDefaults(map[string]int{"vcpus": -2}); err == nil {
		t.Error("limit below -1 accepted")
	}
}
