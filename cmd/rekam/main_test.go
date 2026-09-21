package main

import (
	"testing"

	"github.com/Ucok23/rekam/internal/api"
)

// The solo binary is the one a single person runs for themselves, often on a
// laptop or a home server. Its default must not put an authentication surface
// on whatever network they are attached to; reaching it from elsewhere should
// be a deliberate act. Team and managed exist to be reached, so they bind
// everything.
func TestDefaultListenAddrDependsOnEdition(t *testing.T) {
	got := defaultListenAddr()
	want := ":5000"
	if api.Edition == "solo" {
		want = "127.0.0.1:5000"
	}
	if got != want {
		t.Fatalf("%s edition: default listen address = %q, want %q", api.Edition, got, want)
	}
}
