package fsm

import (
	"fmt"
	"sync"
)

// State is a named state in the machine.
type State string

// Transition defines an allowed move from one state to another.
type Transition struct {
	From State
	To   State
}

// FSM is a thread-safe finite state machine.
type FSM struct {
	mu      sync.RWMutex
	current State
	allowed map[Transition]struct{}
}

// New creates an FSM with the given initial state and allowed transitions.
func New(initial State, transitions []Transition) *FSM {
	allowed := make(map[Transition]struct{}, len(transitions))
	for _, t := range transitions {
		allowed[t] = struct{}{}
	}
	return &FSM{current: initial, allowed: allowed}
}

// State returns the current state.
func (f *FSM) State() State {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.current
}

// Transition moves the FSM to the next state if the transition is allowed.
func (f *FSM) Transition(to State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := Transition{From: f.current, To: to}
	if _, ok := f.allowed[t]; !ok {
		return fmt.Errorf("transition %s → %s not allowed", f.current, to)
	}
	f.current = to
	return nil
}

// ─── Instance lifecycle states ───────────────────────────────────────────────

const (
	InstancePending    State = "pending"
	InstanceScheduling State = "scheduling"
	InstanceScheduled  State = "scheduled"
	InstanceBuilding   State = "building"
	InstanceActive     State = "active"
	InstanceStopping   State = "stopping"
	InstanceStopped    State = "stopped"
	InstanceStarting   State = "starting"
	InstanceDeleting   State = "deleting"
	InstanceDeleted    State = "deleted"
	InstanceError      State = "error"
	InstanceUnknown    State = "unknown" // node unreachable; VM may still be running
)

// InstanceTransitions is the allowed transition table for compute instances.
var InstanceTransitions = []Transition{
	{InstancePending, InstanceScheduling},
	{InstanceScheduling, InstanceScheduled},
	{InstanceScheduling, InstanceError},
	{InstanceScheduled, InstanceBuilding},
	{InstanceBuilding, InstanceActive},
	{InstanceBuilding, InstanceError},
	{InstanceActive, InstanceStopping},
	{InstanceStopping, InstanceStopped},
	{InstanceStopping, InstanceError},
	{InstanceStopped, InstanceStarting},
	{InstanceStarting, InstanceActive},
	{InstanceStarting, InstanceError},
	{InstanceActive, InstanceDeleting},
	{InstanceStopped, InstanceDeleting},
	{InstanceError, InstanceDeleting},
	{InstanceError, InstanceStopped},
	{InstanceDeleting, InstanceDeleted},
	// Node-unreachable transitions: stable states go unknown, unknown recovers when node returns
	{InstanceActive, InstanceUnknown},
	{InstanceStopped, InstanceUnknown},
	{InstanceBuilding, InstanceUnknown},
	{InstanceStarting, InstanceUnknown},
	{InstanceStopping, InstanceUnknown},
	{InstanceUnknown, InstanceActive},
	{InstanceUnknown, InstanceStopped},
	{InstanceUnknown, InstanceError},
	{InstanceUnknown, InstanceDeleting},
}

// ─── Volume lifecycle states ──────────────────────────────────────────────────

const (
	VolumeCreating  State = "creating"
	VolumeAvailable State = "available"
	VolumeAttaching State = "attaching"
	VolumeInUse     State = "in-use"
	VolumeDetaching State = "detaching"
	VolumeDeleting  State = "deleting"
	VolumeDeleted   State = "deleted"
	VolumeError     State = "error"
)

var VolumeTransitions = []Transition{
	{VolumeCreating, VolumeAvailable},
	{VolumeCreating, VolumeError},
	{VolumeAvailable, VolumeAttaching},
	{VolumeAttaching, VolumeInUse},
	{VolumeAttaching, VolumeError},
	{VolumeInUse, VolumeDetaching},
	{VolumeDetaching, VolumeAvailable},
	{VolumeDetaching, VolumeError},
	{VolumeAvailable, VolumeDeleting},
	{VolumeError, VolumeDeleting},
	{VolumeDeleting, VolumeDeleted},
}
