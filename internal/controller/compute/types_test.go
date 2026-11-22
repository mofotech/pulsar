package compute

import (
	"context"
	"strings"
	"testing"
)

func TestValidateHypervisorType(t *testing.T) {
	if err := ValidateHypervisorType(HypervisorKVM); err != nil {
		t.Fatalf("kvm should be supported: %v", err)
	}
	for _, ht := range []HypervisorType{"lxd", "lxc", "containerd", "bogus", ""} {
		err := ValidateHypervisorType(ht)
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("%q: want 'not supported' error, got %v", ht, err)
		}
	}
}

// CreateInstance must reject an unsupported hypervisor before touching any
// store; a zero-value Service has no store, so reaching one would panic.
func TestCreateInstanceRejectsUnsupportedHypervisor(t *testing.T) {
	var s Service
	_, err := s.CreateInstance(context.Background(), "proj", "user", CreateInstanceRequest{
		Name:           "vm",
		FlavorID:       "f",
		ImageID:        "i",
		HypervisorType: "lxd",
	})
	if err == nil || !strings.Contains(err.Error(), `"lxd" is not supported`) {
		t.Fatalf("want unsupported-hypervisor error, got %v", err)
	}
}
