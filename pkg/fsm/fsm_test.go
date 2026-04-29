package fsm

import (
	"testing"
)

func TestTransitionAllowed(t *testing.T) {
	f := New(InstancePending, InstanceTransitions)

	steps := []State{
		InstanceScheduling,
		InstanceScheduled,
		InstanceBuilding,
		InstanceActive,
		InstanceStopping,
		InstanceStopped,
		InstanceDeleting,
		InstanceDeleted,
	}

	for _, next := range steps {
		if err := f.Transition(next); err != nil {
			t.Fatalf("expected transition to %s to succeed, got: %v", next, err)
		}
	}
}

func TestTransitionDenied(t *testing.T) {
	f := New(InstancePending, InstanceTransitions)

	// Cannot jump straight from pending to active
	if err := f.Transition(InstanceActive); err == nil {
		t.Fatal("expected transition pending→active to fail")
	}

	// State must not have changed
	if got := f.State(); got != InstancePending {
		t.Fatalf("state changed after rejected transition: got %s", got)
	}
}

func TestTransitionToError(t *testing.T) {
	f := New(InstanceScheduling, InstanceTransitions)
	if err := f.Transition(InstanceError); err != nil {
		t.Fatalf("scheduling→error should be allowed: %v", err)
	}
	if f.State() != InstanceError {
		t.Fatalf("expected error state, got %s", f.State())
	}
}

func TestVolumeTransitions(t *testing.T) {
	f := New(VolumeCreating, VolumeTransitions)

	if err := f.Transition(VolumeAvailable); err != nil {
		t.Fatalf("creating→available: %v", err)
	}
	if err := f.Transition(VolumeAttaching); err != nil {
		t.Fatalf("available→attaching: %v", err)
	}
	if err := f.Transition(VolumeInUse); err != nil {
		t.Fatalf("attaching→in-use: %v", err)
	}
	if err := f.Transition(VolumeDetaching); err != nil {
		t.Fatalf("in-use→detaching: %v", err)
	}
	if err := f.Transition(VolumeAvailable); err != nil {
		t.Fatalf("detaching→available: %v", err)
	}
}

func TestConcurrentTransitions(t *testing.T) {
	f := New(InstancePending, InstanceTransitions)

	// Only one goroutine should win the first transition
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() { done <- f.Transition(InstanceScheduling) }()
	}

	var successes, failures int
	for i := 0; i < 10; i++ {
		if err := <-done; err == nil {
			successes++
		} else {
			failures++
		}
	}

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful transition, got %d", successes)
	}
	if failures != 9 {
		t.Fatalf("expected 9 failures, got %d", failures)
	}
}
