package xray

import (
	"encoding/json"

	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/routing"
)

// BalancerOverrideResponse is OverrideBalancer's answer: the balancer and the
// outbound it is pinned to now ("" — its strategy picks again), or an error.
type BalancerOverrideResponse struct {
	Balancer string `json:"balancer"`
	Target   string `json:"target"`
	Error    string `json:"error,omitempty"`
}

// OverrideBalancer pins a balancer of the running core to one outbound: every
// new connection the balancer routes goes through target, whatever its strategy
// would pick; connections already open stay where they are. An empty target
// lifts the pin.
//
// Xray does not check that a pinned outbound is alive, and the balancer's
// fallbackTag does not apply to it (app/router/balancing.go, PickOutbound): the
// caller lifts the pin once the observatory finds the outbound dead. It also
// does not check that target is one of the balancer's outbounds — pinning a
// tunnel's balancer to a direct outbound sends its traffic direct — so callers
// pin only to outbounds the balancer selects.
//
// The core is taken as in ProbeOutbound: an atomic snapshot, no race with a
// concurrent StopXray.
func OverrideBalancer(balancer, target string) string {
	resp := BalancerOverrideResponse{Balancer: balancer, Target: target}

	syncOnce.Do(syncCoreServerPtr)
	inst := loadCoreServer()
	if inst == nil || !inst.IsRunning() {
		resp.Error = "xray not running"
		return marshalBalancerOverrideResp(resp)
	}

	// A pin to an outbound the core does not have would send every connection
	// of the balancer to the default outbound, the first in the config.
	if target != "" {
		om, ok := inst.GetFeature(outbound.ManagerType()).(outbound.Manager)
		if !ok || om.GetHandler(target) == nil {
			resp.Error = "outbound '" + target + "' not found"
			return marshalBalancerOverrideResp(resp)
		}
	}

	overrider, ok := inst.GetFeature(routing.RouterType()).(routing.BalancerOverrider)
	if !ok {
		resp.Error = "the router has no balancers"
		return marshalBalancerOverrideResp(resp)
	}
	if err := overrider.SetOverrideTarget(balancer, target); err != nil {
		resp.Error = "balancer '" + balancer + "' not found"
	}
	return marshalBalancerOverrideResp(resp)
}

func marshalBalancerOverrideResp(r BalancerOverrideResponse) string {
	b, err := json.Marshal(r)
	if err != nil {
		return `{"error":"marshal failed"}`
	}
	return string(b)
}
