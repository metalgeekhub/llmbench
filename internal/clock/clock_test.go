package clock

import (
	"testing"
	"time"
)

func TestNowIsFineGrainedAndMonotonic(t *testing.T) {
	// Busy-loop for ~2 ms: consecutive readings must never go backwards, and
	// the clock must resolve steps well below the ~0.5 ms Windows default.
	prev := Now()
	start := prev
	smallest := time.Hour
	for time.Since(start) < 2*time.Millisecond {
		now := Now()
		d := now.Sub(prev)
		if d < 0 {
			t.Fatalf("clock went backwards by %v", d)
		}
		if d > 0 && d < smallest {
			smallest = d
		}
		prev = now
	}
	if smallest > 100*time.Microsecond {
		t.Errorf("clock resolution is %v; want < 100µs", smallest)
	}
	// Close to the wall clock.
	if skew := time.Since(Now()); skew > time.Second || skew < -time.Second {
		t.Errorf("clock is %v off the wall clock", skew)
	}
}
