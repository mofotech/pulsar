// Package quota enforces per-project resource limits.
//
// Limits live in Postgres (the quotas table; rows override the operator's
// defaults). Usage is never stored: it is computed from etcd, the source of
// truth for resources, at check time, so it cannot drift when an async
// create/delete fails. A per-project etcd lock spans "compute usage → check →
// write the new record", so concurrent requests — even across controller
// replicas — cannot both squeeze under a limit.
package quota

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.uber.org/zap"
)

// Resource is a quota-limited resource class.
type Resource string

const (
	Instances   Resource = "instances"
	VCPUs       Resource = "vcpus"
	RAMMB       Resource = "ram_mb"
	Volumes     Resource = "volumes"
	StorageGB   Resource = "storage_gb"
	FloatingIPs Resource = "floating_ips"
	Networks    Resource = "networks"
	Routers     Resource = "routers"
)

// Unlimited as a limit disables enforcement for that resource.
const Unlimited = -1

// All lists every resource in display order.
var All = []Resource{Instances, VCPUs, RAMMB, Volumes, StorageGB, FloatingIPs, Networks, Routers}

// BuiltinDefaults apply when neither the operator config nor a per-project
// row sets a limit. They match OpenStack's long-standing defaults.
var BuiltinDefaults = Limits{
	Instances:   10,
	VCPUs:       20,
	RAMMB:       51200,
	Volumes:     10,
	StorageGB:   1000,
	FloatingIPs: 10,
	Networks:    10,
	Routers:     10,
}

// Limits maps resources to hard limits (Unlimited = no limit).
type Limits map[Resource]int

// Usage maps resources to amounts (current usage, or a requested delta).
type Usage map[Resource]int

// Valid reports whether r is a known resource.
func Valid(r Resource) bool {
	return slices.Contains(All, r)
}

// ParseDefaults validates operator-supplied defaults (controller.quota_defaults)
// and merges them over BuiltinDefaults.
func ParseDefaults(cfg map[string]int) (Limits, error) {
	out := Limits{}
	maps.Copy(out, BuiltinDefaults)
	for k, v := range cfg {
		r := Resource(strings.ToLower(k))
		if !Valid(r) {
			return nil, fmt.Errorf("quota_defaults: unknown resource %q (valid: %s)", k, names())
		}
		if v < Unlimited {
			return nil, fmt.Errorf("quota_defaults: %s must be >= -1 (-1 = unlimited)", k)
		}
		out[r] = v
	}
	return out, nil
}

func names() string {
	s := make([]string, len(All))
	for i, r := range All {
		s[i] = string(r)
	}
	return strings.Join(s, ", ")
}

// ExceededError reports a request that would take a project over a limit.
type ExceededError struct {
	Resource  Resource
	Requested int
	Used      int
	Limit     int
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("quota exceeded for %s: requested %d, %d of %d in use",
		e.Resource, e.Requested, e.Used, e.Limit)
}

// IsExceeded reports whether err is (or wraps) an ExceededError.
func IsExceeded(err error) bool {
	var e *ExceededError
	return errors.As(err, &e)
}

// UsageFunc reports a project's current usage for the resources it owns.
// Each service (compute, storage, network) registers one.
type UsageFunc func(ctx context.Context, projectID string) (Usage, error)

// LimitStore loads a project's effective limits.
type LimitStore interface {
	Limits(ctx context.Context, projectID string) (Limits, error)
}

// Locker provides a distributed mutex (the etcd client implements it).
type Locker interface {
	Lock(ctx context.Context, key string) (func(), error)
}

// Enforcer checks requests against limits. A nil *Enforcer allows everything,
// which keeps services usable in tests and tools that don't wire quotas.
type Enforcer struct {
	limits LimitStore
	locker Locker
	log    *zap.Logger
	usage  []UsageFunc
}

func NewEnforcer(limits LimitStore, locker Locker, log *zap.Logger) *Enforcer {
	return &Enforcer{limits: limits, locker: locker, log: log}
}

// RegisterUsage adds a usage source. Call during start-up, before serving.
func (e *Enforcer) RegisterUsage(fn UsageFunc) {
	e.usage = append(e.usage, fn)
}

// Usage sums every registered source for a project.
func (e *Enforcer) Usage(ctx context.Context, projectID string) (Usage, error) {
	total := Usage{}
	for _, fn := range e.usage {
		u, err := fn(ctx, projectID)
		if err != nil {
			return nil, err
		}
		for r, n := range u {
			total[r] += n
		}
	}
	return total, nil
}

// Reserve checks that adding want to the project's current usage stays
// within its limits and, if so, runs commit — all under the project's lock,
// so commit must persist the resource record (making it visible to the next
// usage computation) before returning. Non-positive deltas are not checked.
func (e *Enforcer) Reserve(ctx context.Context, projectID string, want Usage, commit func() error) error {
	if e == nil {
		return commit()
	}
	unlock, err := e.locker.Lock(ctx, "/pulsar/locks/quota/"+projectID)
	if err != nil {
		return fmt.Errorf("quota lock: %w", err)
	}
	defer unlock()

	if err := e.check(ctx, projectID, want); err != nil {
		return err
	}
	return commit()
}

// Check validates want without reserving anything. Use it for operations
// whose resource record is only updated after a long agent call (resize,
// extend), where holding the project lock would stall other requests.
func (e *Enforcer) Check(ctx context.Context, projectID string, want Usage) error {
	if e == nil {
		return nil
	}
	return e.check(ctx, projectID, want)
}

func (e *Enforcer) check(ctx context.Context, projectID string, want Usage) error {
	limits, err := e.limits.Limits(ctx, projectID)
	if err != nil {
		return fmt.Errorf("load quota limits: %w", err)
	}
	used, err := e.Usage(ctx, projectID)
	if err != nil {
		return fmt.Errorf("compute quota usage: %w", err)
	}
	// Deterministic order so the reported resource is stable.
	rs := make([]Resource, 0, len(want))
	for r := range want {
		rs = append(rs, r)
	}
	slices.Sort(rs)
	for _, r := range rs {
		n := want[r]
		limit, ok := limits[r]
		if n <= 0 || !ok || limit == Unlimited {
			continue
		}
		if used[r]+n > limit {
			return &ExceededError{Resource: r, Requested: n, Used: used[r], Limit: limit}
		}
	}
	return nil
}
