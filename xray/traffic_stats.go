package xray

import (
	"encoding/json"
	"strings"

	"github.com/xtls/xray-core/features/stats"
)

// TrafficCounter is the bytes one outbound carried since its core started.
type TrafficCounter struct {
	Uplink   int64 `json:"uplink"`
	Downlink int64 `json:"downlink"`
}

// TrafficStatsResponse is what GetTrafficStats returns: the outbounds by
// tag, never null, and an error when there is no core to read.
type TrafficStatsResponse struct {
	Outbound map[string]TrafficCounter `json:"outbound"`
	Error    string                    `json:"error,omitempty"`
}

// The names an outbound handler gives its counters
// (app/proxyman/outbound/handler.go, getStatCounter).
const (
	outboundCounterPrefix = "outbound>>>"
	uplinkCounterSuffix   = ">>>traffic>>>uplink"
	downlinkCounterSuffix = ">>>traffic>>>downlink"
)

// GetTrafficStats returns the bytes of every outbound of the running core
// since it started, read from its stats manager in this process: no API
// inbound and no metrics listener, so it answers for every start (Xray's
// metrics are made once a process: a second one panics in expvar.Publish).
//
// An outbound has counters when the config has "stats" and
// policy.system.statsOutboundUplink/Downlink; without them the map is empty.
// A new core counts from zero. The counters are only read, never reset.
func GetTrafficStats() string {
	resp := TrafficStatsResponse{Outbound: map[string]TrafficCounter{}}
	syncOnce.Do(syncCoreServerPtr)
	inst := loadCoreServer()
	if inst == nil || !inst.IsRunning() {
		resp.Error = "xray not running"
		return marshalTrafficStats(resp)
	}
	manager, ok := inst.GetFeature(stats.ManagerType()).(stats.Manager)
	if !ok {
		return marshalTrafficStats(resp)
	}
	manager.VisitCounters(func(name string, counter stats.Counter) bool {
		rest, found := strings.CutPrefix(name, outboundCounterPrefix)
		if !found {
			return true
		}
		if tag, up := strings.CutSuffix(rest, uplinkCounterSuffix); up {
			c := resp.Outbound[tag]
			c.Uplink = counter.Value()
			resp.Outbound[tag] = c
		} else if tag, down := strings.CutSuffix(rest, downlinkCounterSuffix); down {
			c := resp.Outbound[tag]
			c.Downlink = counter.Value()
			resp.Outbound[tag] = c
		}
		return true
	})
	return marshalTrafficStats(resp)
}

func marshalTrafficStats(resp TrafficStatsResponse) string {
	out, err := json.Marshal(resp)
	if err != nil {
		return `{"outbound":{},"error":"marshal failed"}`
	}
	return string(out)
}
