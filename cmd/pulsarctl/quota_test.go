package main

import "testing"

func TestParseQuotaArgs(t *testing.T) {
	got, err := parseQuotaArgs([]string{"vcpus=40", "instances=unlimited", "volumes=default", "routers=-1"})
	if err != nil {
		t.Fatal(err)
	}
	if *got["vcpus"] != 40 || *got["instances"] != -1 || got["volumes"] != nil || *got["routers"] != -1 {
		t.Fatalf("got %v", got)
	}
	if _, ok := got["volumes"]; !ok {
		t.Fatal("default must be sent as null, not omitted")
	}
	for _, bad := range []string{"vcpus", "vcpus=", "=4", "vcpus=lots"} {
		if _, err := parseQuotaArgs([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
