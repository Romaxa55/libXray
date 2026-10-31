package xray

import (
	"encoding/json"
	"strings"

	"github.com/xtls/xray-core/features/stats"
)

// OutboundTraffic is one outbound's bytes since the core started.
type OutboundTraffic struct {
	Uplink   int64 `json:"uplink"`
	Downlink int64 `json:"downlink"`
}

// OutboundStatsResponse is GetOutboundStats' answer: the traffic of each
// outbound the core counts, by tag, or an error.
type OutboundStatsResponse struct {
	Outbounds map[string]OutboundTraffic `json:"outbounds"`
	Error     string                     `json:"error,omitempty"`
}

// GetOutboundStats reads the traffic counters of the running core's
// outbounds from inside the instance: bytes up and down per outbound tag
// since the core started. The core counts only with "stats": {} and
// policy.system.statsOutboundUplink and statsOutboundDownlink in its config;
// without them the map is empty. Reading resets nothing.
//
// The way an app inside one process gets the counters: the metrics server
// (metrics.listen, QueryStats) publishes its expvars on every start of a
// core, and the second start in a process panics on the reused name.
//
// The core is taken as in ProbeOutbound: an atomic snapshot, no race with a
// concurrent StopXray.
func GetOutboundStats() string {
	resp := OutboundStatsResponse{Outbounds: map[string]OutboundTraffic{}}

	syncOnce.Do(syncCoreServerPtr)
	inst := loadCoreServer()
	if inst == nil || !inst.IsRunning() {
		resp.Error = "xray not running"
		return marshalOutboundStatsResp(resp)
	}

	manager, ok := inst.GetFeature(stats.ManagerType()).(stats.Manager)
	if !ok {
		resp.Error = "stats not configured"
		return marshalOutboundStatsResp(resp)
	}
	// Counters are named "outbound>>>TAG>>>traffic>>>uplink|downlink".
	manager.VisitCounters(func(name string, counter stats.Counter) bool {
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[0] != "outbound" || parts[2] != "traffic" {
			return true
		}
		traffic := resp.Outbounds[parts[1]]
		switch parts[3] {
		case "uplink":
			traffic.Uplink = counter.Value()
		case "downlink":
			traffic.Downlink = counter.Value()
		default:
			return true
		}
		resp.Outbounds[parts[1]] = traffic
		return true
	})
	return marshalOutboundStatsResp(resp)
}

func marshalOutboundStatsResp(r OutboundStatsResponse) string {
	b, err := json.Marshal(r)
	if err != nil {
		return `{"outbounds":{},"error":"marshal failed"}`
	}
	return string(b)
}
