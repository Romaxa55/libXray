package libv2ray

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// exit is the site one outbound leads to: it answers with its name or, once
// down, takes the connection and never answers — an exit that accepts and
// carries nothing.
type exit struct {
	name   string
	down   atomic.Bool
	server *httptest.Server
}

func newExit(t *testing.T, name string) *exit {
	t.Helper()
	e := &exit{name: name}
	release := make(chan struct{})
	e.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e.down.Load() {
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		_, _ = io.WriteString(w, e.name)
	}))
	t.Cleanup(e.server.Close)
	// Cleanups run last first: the held requests end before Close waits for them.
	t.Cleanup(func() { close(release) })
	return e
}

func (e *exit) addr() string { return strings.TrimPrefix(e.server.URL, "http://") }

// balancedConfig is a SOCKS inbound whose connections go to the balancer
// "exits" (leastPing, fallback exit-0) over exit-0 and exit-1: freedom
// outbounds that send every connection to a and b. The burstObservatory probes
// them at the start and then once an hour, so in a test only ObservatoryCheck
// probes them again.
func balancedConfig(t *testing.T, socks int, a, b *exit) string {
	t.Helper()
	to := func(tag string, e *exit) map[string]any {
		return map[string]any{
			"tag": tag, "protocol": "freedom",
			"settings": map[string]any{"redirect": e.addr()},
		}
	}
	out, err := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{
			"tag": "socks-in", "listen": "127.0.0.1", "port": socks, "protocol": "socks",
		}},
		"outbounds": []any{
			to("exit-0", a),
			to("exit-1", b),
			map[string]any{"tag": "direct", "protocol": "freedom"},
		},
		"routing": map[string]any{
			"balancers": []any{map[string]any{
				"tag": "exits", "selector": []string{"exit-"},
				"strategy": map[string]any{"type": "leastPing"}, "fallbackTag": "exit-0",
			}},
			"rules": []any{map[string]any{
				"type": "field", "inboundTag": []string{"socks-in"}, "balancerTag": "exits",
			}},
		},
		"burstObservatory": map[string]any{
			"subjectSelector": []string{"exit-"},
			"pingConfig": map[string]any{
				"destination": "http://probe.test/", "interval": "1h", "sampling": 1, "timeout": "1s",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// client gets pages through the core's SOCKS inbound; keepAlive reuses its
// connection, else every page is a new one.
func client(socks int, keepAlive bool) *http.Client {
	proxy := &url.URL{Scheme: "socks5", Host: fmt.Sprintf("127.0.0.1:%d", socks)}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: !keepAlive},
		Timeout:   5 * time.Second,
	}
}

// exitOf is the name of the exit a page through c came from.
func exitOf(t *testing.T, c *http.Client) string {
	t.Helper()
	resp, err := c.Get("http://site.test/")
	if err != nil {
		t.Fatalf("a page through the core: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("a page through the core: %v", err)
	}
	return string(body)
}

type overrideAnswer struct {
	Balancer string `json:"balancer"`
	Target   string `json:"target"`
	Error    string `json:"error"`
}

func override(t *testing.T, balancer, target string) overrideAnswer {
	t.Helper()
	var r overrideAnswer
	if err := json.Unmarshal([]byte(OverrideBalancer(balancer, target)), &r); err != nil {
		t.Fatalf("OverrideBalancer: %v", err)
	}
	return r
}

type observation struct {
	Nodes []struct {
		Tag   string `json:"tag"`
		Alive *bool  `json:"alive"`
	} `json:"nodes"`
	Error string `json:"error"`
}

func observationOf(t *testing.T, raw string) observation {
	t.Helper()
	var o observation
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatalf("observatory state %q: %v", raw, err)
	}
	return o
}

// alive is "true", "false" or "unknown" for tag: unknown until a probe of it
// has ended.
func (o observation) alive(tag string) string {
	for _, n := range o.Nodes {
		if n.Tag == tag && n.Alive != nil {
			return fmt.Sprint(*n.Alive)
		}
	}
	return "unknown"
}

func startBalanced(t *testing.T) (socks int, a, b *exit) {
	t.Helper()
	a, b = newExit(t, "a"), newExit(t, "b")
	socks = freePort(t)
	t.Cleanup(func() { StopV2Ray() })
	if got := StartV2RayWithConfig(balancedConfig(t, socks, a, b)); got != "SUCCESS" {
		t.Fatalf("start: %s", got)
	}
	return socks, a, b
}

func TestOverrideBalancerPinsNewConnections(t *testing.T) {
	socks, _, _ := startBalanced(t)
	fresh := client(socks, false)
	name := map[string]string{"exit-0": "a", "exit-1": "b"}
	for _, target := range []string{"exit-1", "exit-0", "exit-1"} {
		if r := override(t, "exits", target); r.Error != "" || r.Target != target {
			t.Fatalf("pin to %s: %+v", target, r)
		}
		for i := 0; i < 3; i++ {
			if got := exitOf(t, fresh); got != name[target] {
				t.Fatalf("pinned to %s, a new connection came from %q", target, got)
			}
		}
	}

	// A connection open before a new pin stays on its exit.
	kept := client(socks, true)
	if got := exitOf(t, kept); got != "b" {
		t.Fatalf("pinned to exit-1, came from %q", got)
	}
	override(t, "exits", "exit-0")
	if got := exitOf(t, kept); got != "b" {
		t.Fatalf("an open connection moved to %q with the pin", got)
	}
	if got := exitOf(t, fresh); got != "a" {
		t.Fatalf("pinned to exit-0, a new connection came from %q", got)
	}

	if r := override(t, "exits", ""); r.Error != "" {
		t.Fatalf("lifting the pin: %s", r.Error)
	}
	if r := override(t, "nope", "exit-0"); r.Error != "balancer 'nope' not found" {
		t.Fatalf("an unknown balancer: %+v", r)
	}
	if r := override(t, "exits", "exit-9"); r.Error != "outbound 'exit-9' not found" {
		t.Fatalf("an unknown outbound: %+v", r)
	}
}

func TestObservatoryCheckFindsADeadExitWithinTheTimeout(t *testing.T) {
	socks, _, b := startBalanced(t)
	// The round at the start: both exits answer.
	deadline := time.Now().Add(5 * time.Second)
	for {
		o := observationOf(t, GetObservatoryState(""))
		if o.alive("exit-0") == "true" && o.alive("exit-1") == "true" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the round at the start: %+v", o)
		}
		time.Sleep(50 * time.Millisecond)
	}

	b.down.Store(true)
	began := time.Now()
	o := observationOf(t, ObservatoryCheck())
	took := time.Since(began)
	if o.Error != "" || o.alive("exit-0") != "true" || o.alive("exit-1") != "false" {
		t.Fatalf("after exit-1 went down: %+v", o)
	}
	// pingConfig.timeout is 1 s; the rest is slack for a slow machine.
	if took > 3*time.Second {
		t.Fatalf("the round took %v", took)
	}
	fresh := client(socks, false)
	for i := 0; i < 3; i++ {
		if got := exitOf(t, fresh); got != "a" {
			t.Fatalf("exit-1 is dead, a new connection came from %q", got)
		}
	}

	b.down.Store(false)
	if o := observationOf(t, ObservatoryCheck()); o.alive("exit-1") != "true" {
		t.Fatalf("after exit-1 came back: %+v", o)
	}
}

func TestBalancerCallsNeedABalancedCore(t *testing.T) {
	t.Cleanup(func() { StopV2Ray() })
	StopV2Ray()
	if r := override(t, "exits", ""); r.Error != "xray not running" {
		t.Fatalf("pin with no core: %+v", r)
	}
	if o := observationOf(t, ObservatoryCheck()); o.Error != "xray not running" {
		t.Fatalf("check with no core: %+v", o)
	}

	if got := StartV2RayWithConfig(config(t, false)); got != "SUCCESS" {
		t.Fatalf("start: %s", got)
	}
	if r := override(t, "exits", "direct"); r.Error != "balancer 'exits' not found" {
		t.Fatalf("pin with no balancer: %+v", r)
	}
	if o := observationOf(t, ObservatoryCheck()); o.Error != "no burstObservatory in the config" {
		t.Fatalf("check with no observatory: %+v", o)
	}
}
