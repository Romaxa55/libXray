package memory

import (
	"runtime"
	"testing"
	"time"
)

// goroutinesDownTo waits up to two seconds for the goroutine count to come
// down to want and returns the last count.
func goroutinesDownTo(want int) int {
	deadline := time.Now().Add(2 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= want || time.Now().After(deadline) {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func tickerRuns() bool {
	tickerMu.Lock()
	defer tickerMu.Unlock()
	return tickerStop != nil
}

func TestRepeatedStartsKeepOneTicker(t *testing.T) {
	t.Cleanup(StopForceFree)
	before := runtime.NumGoroutine()
	for i := 0; i < 23; i++ {
		startTicker(time.Hour)
	}
	if !tickerRuns() {
		t.Fatal("no ticker after 23 starts")
	}
	if got := runtime.NumGoroutine(); got > before+1 {
		t.Fatalf("23 starts: %d goroutines, %d before", got, before)
	}
	StopForceFree()
	if tickerRuns() {
		t.Fatal("a ticker after stop")
	}
	if got := goroutinesDownTo(before); got > before {
		t.Fatalf("after stop: %d goroutines, %d before", got, before)
	}
}

func TestStartStopCyclesLeaveNoTicker(t *testing.T) {
	t.Cleanup(StopForceFree)
	before := runtime.NumGoroutine()
	for i := 0; i < 23; i++ {
		startTicker(time.Hour)
		StopForceFree()
	}
	if tickerRuns() {
		t.Fatal("a ticker after the last stop")
	}
	if got := goroutinesDownTo(before); got > before {
		t.Fatalf("23 start/stop cycles: %d goroutines, %d before", got, before)
	}
}

func TestStopWithoutStart(t *testing.T) {
	StopForceFree()
	StopForceFree()
	if tickerRuns() {
		t.Fatal("a ticker without a start")
	}
}
