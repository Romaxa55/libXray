package memory

import (
	"runtime"
	"testing"
	"time"
)

// goroutinesSettleTo waits for the goroutine count to come down to want.
func goroutinesSettleTo(t *testing.T, want int) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= want || time.Now().After(deadline) {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRepeatedStartsKeepOneTicker(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 23; i++ {
		startTicker(time.Hour)
	}
	if got := runtime.NumGoroutine(); got != before+1 {
		t.Fatalf("23 starts: %d goroutines, want %d", got, before+1)
	}
	StopForceFree()
	if got := goroutinesSettleTo(t, before); got != before {
		t.Fatalf("after stop: %d goroutines, want %d", got, before)
	}
}

func TestStartStopCyclesLeaveNoTicker(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 23; i++ {
		startTicker(time.Hour)
		StopForceFree()
	}
	if got := goroutinesSettleTo(t, before); got != before {
		t.Fatalf("23 start/stop cycles: %d goroutines, want %d", got, before)
	}
}

func TestStopWithoutStart(t *testing.T) {
	StopForceFree()
	StopForceFree()
}
