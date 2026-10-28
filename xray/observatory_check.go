package xray

import (
	"strings"
	"sync"
	"time"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/infra/conf/serial"
)

// observed is the running core and the subjectSelector of its
// burstObservatory. The observatory keeps its selector to itself
// (app/observatory/burst, Observer.config), so the core's start remembers it
// from the config.
var observed struct {
	sync.Mutex
	inst     *core.Instance
	selector []string
}

// rememberObserved keeps the burstObservatory selector of configJSON for inst.
// A config without a burstObservatory, or one that does not decode, keeps none.
func rememberObserved(inst *core.Instance, configJSON string) {
	var selector []string
	c, err := serial.DecodeJSONConfig(strings.NewReader(configJSON))
	if err == nil && c.BurstObservatory != nil {
		selector = c.BurstObservatory.SubjectSelector
	}
	observed.Lock()
	observed.inst, observed.selector = inst, selector
	observed.Unlock()
}

// forgetObserved drops the stopped core, so it is not kept from the GC.
func forgetObserved() {
	observed.Lock()
	observed.inst, observed.selector = nil, nil
	observed.Unlock()
}

// ObservatoryCheck runs one round of the burstObservatory's probes now, over
// the outbounds its subjectSelector picks — the same ones its scheduled rounds
// probe — and answers with GetObservatoryState once every probe has answered
// or run out of pingConfig.timeout: an outbound that stopped answering reads
// alive=false within that timeout, and a balancer's leastPing passes it by from
// then on, without waiting for the next scheduled round. When pingConfig sets
// connectivity and the device has no network, the round records nothing.
//
// It blocks for up to the timeout, so callers run it off the main thread. The
// core is taken as in ProbeOutbound: an atomic snapshot, no race with a
// concurrent StopXray.
func ObservatoryCheck() string {
	syncOnce.Do(syncCoreServerPtr)
	inst := loadCoreServer()
	if inst == nil || !inst.IsRunning() {
		return observatoryCheckError("xray not running")
	}
	obs, ok := inst.GetFeature(extension.ObservatoryType()).(extension.BurstObservatory)
	if !ok {
		return observatoryCheckError("no burstObservatory in the config")
	}
	hs, ok := inst.GetFeature(outbound.ManagerType()).(outbound.HandlerSelector)
	if !ok {
		return observatoryCheckError("outbound manager is not a HandlerSelector")
	}

	observed.Lock()
	var selector []string
	if observed.inst == inst {
		selector = observed.selector
	}
	observed.Unlock()

	tags := hs.Select(selector)
	if len(tags) == 0 {
		return observatoryCheckError("the burstObservatory selects no outbound")
	}
	obs.Check(tags)
	return GetObservatoryState("")
}

func observatoryCheckError(reason string) string {
	return marshalObservatoryResp(ObservatoryStateResponse{
		TimestampMs: time.Now().UnixMilli(),
		Error:       reason,
	})
}
