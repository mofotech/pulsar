package main

import (
	"testing"
	"time"
)

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

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]string{
		"24h":                  "2026-11-20T12:00:00Z",
		"7d":                   "2026-11-14T12:00:00Z",
		"90m":                  "2026-11-21T10:30:00Z",
		"2026-11-01T00:00:00Z": "2026-11-01T00:00:00Z",
	} {
		got, err := parseSince(in, now)
		if err != nil || got != want {
			t.Errorf("parseSince(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := parseSince("yesterday", now); err == nil {
		t.Error("accepted 'yesterday'")
	}
}
