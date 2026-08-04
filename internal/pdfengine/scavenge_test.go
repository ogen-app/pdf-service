package pdfengine

import (
	"testing"
	"time"
)

// TestRequestDone_ScavengesOnDrainToIdle checks that only the call draining the
// in-flight count to zero triggers a scavenge — an overlapping call must not.
// The engine is built directly (no pool) so the test needs no native pdfium.
func TestRequestDone_ScavengesOnDrainToIdle(t *testing.T) {
	scavenged := make(chan struct{}, 4)
	e := &Engine{
		scavengeOnIdle: true,
		scavengeFn:     func() { scavenged <- struct{}{} },
	}
	// Two overlapping requests, tracked the same way Extract/Render do.
	e.active.Add(1)
	e.active.Add(1)

	e.requestDone() // 2 -> 1: still busy, must not scavenge
	select {
	case <-scavenged:
		t.Fatal("scavenged while a request was still active")
	case <-time.After(50 * time.Millisecond):
	}

	e.requestDone() // 1 -> 0: drained to idle, must scavenge
	select {
	case <-scavenged:
	case <-time.After(2 * time.Second):
		t.Fatal("expected a scavenge after draining to idle")
	}
}

// TestRequestDone_NoScavengeWhenDisabled confirms the knob gates the behavior.
func TestRequestDone_NoScavengeWhenDisabled(t *testing.T) {
	scavenged := make(chan struct{}, 1)
	e := &Engine{
		scavengeOnIdle: false,
		scavengeFn:     func() { scavenged <- struct{}{} },
	}
	e.active.Add(1)
	e.requestDone()

	select {
	case <-scavenged:
		t.Error("scavenge ran though ScavengeOnIdle is false")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestScavenge_SingleFlight verifies overlapping triggers collapse into one
// in-flight scavenge rather than piling up stop-the-world GCs.
func TestScavenge_SingleFlight(t *testing.T) {
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	e := &Engine{
		scavengeOnIdle: true,
		scavengeFn: func() {
			started <- struct{}{}
			<-release // hold the scavenge open so the guard stays set
		},
	}

	e.scavenge()
	e.scavenge() // dropped while the first is still running

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first scavenge never started")
	}
	select {
	case <-started:
		t.Fatal("second scavenge ran despite single-flight guard")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
}
