package memory

import (
	"runtime/debug"
	"sync"
	"time"
)

// The FreeOSMemory ticker lives while a core runs: every core start used to
// spawn one more ticker goroutine and none ever ended, so a process that
// restarts its core (a new exit, a reconnect) piled up tickers, each forcing
// a GC every second.
var (
	tickerMu   sync.Mutex
	tickerStop chan struct{}
)

// startTicker runs debug.FreeOSMemory every interval until StopForceFree;
// while a ticker runs, another start changes nothing.
func startTicker(interval time.Duration) {
	tickerMu.Lock()
	defer tickerMu.Unlock()
	if tickerStop != nil {
		return
	}
	stop := make(chan struct{})
	tickerStop = stop
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				debug.FreeOSMemory()
			case <-stop:
				return
			}
		}
	}()
}

// StopForceFree ends the ticker InitForceFree started, if any. The GC
// settings stay: they belong to the process, not to one core.
func StopForceFree() {
	tickerMu.Lock()
	defer tickerMu.Unlock()
	if tickerStop != nil {
		close(tickerStop)
		tickerStop = nil
	}
}
