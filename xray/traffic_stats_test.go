package xray

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// bodyBytes is what the test server answers: enough to tell the page from
// the request headers that go the other way.
const bodyBytes = 64 << 10

// trafficPort is a TCP port on 127.0.0.1 nobody listens on.
func trafficPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// trafficConfig is an HTTP inbound on 127.0.0.1:port, a "direct" freedom
// that carries everything and a "block" nothing goes to; with stats the
// core counts the bytes of each outbound.
func trafficConfig(t *testing.T, port int, withStats bool) string {
	t.Helper()
	config := map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{
			"tag": "http-in", "listen": "127.0.0.1", "port": port, "protocol": "http",
		}},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "block", "protocol": "blackhole"},
		},
	}
	if withStats {
		config["stats"] = map[string]any{}
		config["policy"] = map[string]any{"system": map[string]any{
			"statsOutboundUplink": true, "statsOutboundDownlink": true,
		}}
	}
	out, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// startTraffic starts a core with trafficConfig and returns its inbound's
// port; the core stops when the test ends.
func startTraffic(t *testing.T, withStats bool) int {
	t.Helper()
	port := trafficPort(t)
	if err := RunXrayFromJSON("", "", trafficConfig(t, port, withStats)); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { StopXray() })
	return port
}

// fetchThrough gets the server's page through the core's HTTP inbound.
func fetchThrough(t *testing.T, port int, server *httptest.Server) {
	t.Helper()
	proxy, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true},
		Timeout:   10 * time.Second,
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("get through the core: %v", err)
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil || n != bodyBytes {
		t.Fatalf("body: %d bytes, %v", n, err)
	}
}

func readTraffic(t *testing.T) (TrafficStatsResponse, string) {
	t.Helper()
	raw := GetTrafficStats()
	var resp TrafficStatsResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("GetTrafficStats %q: %v", raw, err)
	}
	return resp, raw
}

// directGrown waits up to two seconds for direct's downlink to reach want:
// the core adds a connection's bytes as they pass, the last ones may land
// just after the client has read them.
func directGrown(t *testing.T, want int64) TrafficStatsResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, _ := readTraffic(t)
		if resp.Outbound["direct"].Downlink >= want || time.Now().After(deadline) {
			return resp
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func pageServer(t *testing.T) *httptest.Server {
	t.Helper()
	page := strings.Repeat("z", bodyBytes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, page)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestTrafficStatsWithoutCore(t *testing.T) {
	StopXray()
	resp, raw := readTraffic(t)
	if resp.Error != "xray not running" {
		t.Fatalf("error %q, want xray not running", resp.Error)
	}
	if resp.Outbound == nil || len(resp.Outbound) != 0 || !strings.Contains(raw, `"outbound":{}`) {
		t.Fatalf("outbound must be an empty object: %s", raw)
	}
}

func TestTrafficStatsCountEachOutbound(t *testing.T) {
	server := pageServer(t)
	port := startTraffic(t, true)

	resp, raw := readTraffic(t)
	if resp.Error != "" {
		t.Fatalf("running core: %s", raw)
	}
	for _, tag := range []string{"direct", "block"} {
		if _, ok := resp.Outbound[tag]; !ok {
			t.Fatalf("no counters of %q before any traffic: %s", tag, raw)
		}
	}

	fetchThrough(t, port, server)
	resp = directGrown(t, bodyBytes)
	direct := resp.Outbound["direct"]
	if direct.Downlink < bodyBytes || direct.Downlink > 2*bodyBytes {
		t.Fatalf("direct downlink %d, want about %d", direct.Downlink, bodyBytes)
	}
	if direct.Uplink <= 0 || direct.Uplink >= bodyBytes {
		t.Fatalf("direct uplink %d: the request, not the page", direct.Uplink)
	}
	if block := resp.Outbound["block"]; block.Uplink != 0 || block.Downlink != 0 {
		t.Fatalf("block carried nothing, counted %+v", block)
	}
}

func TestTrafficStatsNewCoreCountsFromZero(t *testing.T) {
	server := pageServer(t)
	fetchThrough(t, startTraffic(t, true), server)
	if got := directGrown(t, bodyBytes).Outbound["direct"].Downlink; got < bodyBytes {
		t.Fatalf("first core: direct downlink %d", got)
	}
	StopXray()

	port := startTraffic(t, true)
	resp, raw := readTraffic(t)
	if resp.Error != "" || resp.Outbound["direct"].Downlink != 0 {
		t.Fatalf("a new core starts from zero: %s", raw)
	}
	fetchThrough(t, port, server)
	if got := directGrown(t, bodyBytes).Outbound["direct"].Downlink; got < bodyBytes {
		t.Fatalf("second core: direct downlink %d", got)
	}
}

func TestTrafficStatsWithoutStatsConfig(t *testing.T) {
	server := pageServer(t)
	fetchThrough(t, startTraffic(t, false), server)
	resp, raw := readTraffic(t)
	if resp.Error != "" || len(resp.Outbound) != 0 {
		t.Fatalf("a config without stats counts nothing, no error: %s", raw)
	}
}
