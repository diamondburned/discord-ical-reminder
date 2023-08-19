package clocker

import (
	"testing"
	"time"
)

func TestTicker(t *testing.T) {
	const d = 5

	ticker := NewTicker(d * time.Second)
	defer ticker.Stop()

	tick := <-ticker.C

	// Guarantee that the tick is round.
	if tick.Second()%d != 0 {
		t.Fatalf("Tick second is not a multiple of %d: %d", d, tick.Second())
	}

	latency := tick.Sub(tick.Truncate(d * time.Second))
	t.Logf("Ticker latency: %s", latency)
}

func TestTickerDrift(t *testing.T) {
	ticker := NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var last time.Time
	for i := 0; i < 10; i++ {
		last = <-ticker.C
	}

	// Guarantee that the tick is round.
	ms := time.Duration(last.Nanosecond()) / time.Millisecond
	if ms%200 != 0 {
		t.Fatalf("Tick millisecond is not a multiple of 200: %d", ms)
	}
}
