//go:build ios

package memory

import (
	"runtime/debug"

	"time"
)

const (
	interval = 1
	// 30M
	maxMemory = 30 * 1024 * 1024
)

// InitForceFree tunes the GC for the Network Extension's memory cap and starts
// the FreeOSMemory ticker; called on every core start, it keeps one ticker per
// running core (StopForceFree ends it on stop).
func InitForceFree() {
	debug.SetGCPercent(10)
	debug.SetMemoryLimit(maxMemory)
	startTicker(time.Duration(interval) * time.Second)
}
