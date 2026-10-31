package libv2ray

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const pageSize = 64 << 10

// countedConfig is a SOCKS inbound whose connections go to exit-0, a freedom
// outbound that sends them to site; with counting, the core keeps the
// outbounds' traffic.
func countedConfig(t *testing.T, socks int, site *httptest.Server, counting bool) string {
	t.Helper()
	config := map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{
			"tag": "socks-in", "listen": "127.0.0.1", "port": socks, "protocol": "socks",
		}},
		"outbounds": []any{
			map[string]any{
				"tag": "exit-0", "protocol": "freedom",
				"settings": map[string]any{"redirect": strings.TrimPrefix(site.URL, "http://")},
			},
			map[string]any{"tag": "direct", "protocol": "freedom"},
		},
		"routing": map[string]any{"rules": []any{map[string]any{
			"type": "field", "inboundTag": []string{"socks-in"}, "outboundTag": "exit-0",
		}}},
	}
	if counting {
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

type outboundStats struct {
	Outbounds map[string]struct {
		Uplink   int64 `json:"uplink"`
		Downlink int64 `json:"downlink"`
	} `json:"outbounds"`
	Error string `json:"error"`
}

func statsNow(t *testing.T) outboundStats {
	t.Helper()
	raw := GetOutboundStats()
	var s outboundStats
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("GetOutboundStats %q: %v", raw, err)
	}
	return s
}

func fetchPage(t *testing.T, socks int) {
	t.Helper()
	resp, err := client(socks, false).Get("http://site.test/")
	if err != nil {
		t.Fatalf("a page through the core: %v", err)
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil || n != pageSize {
		t.Fatalf("a page through the core: %d bytes, %v", n, err)
	}
}

func startCounted(t *testing.T, socks int, site *httptest.Server, counting bool) {
	t.Helper()
	if got := StartV2RayWithConfig(countedConfig(t, socks, site, counting)); got != "SUCCESS" {
		t.Fatalf("start: %s", got)
	}
}

func TestGetOutboundStatsCountsEachStartFromZero(t *testing.T) {
	page := strings.Repeat("z", pageSize)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, page)
	}))
	t.Cleanup(site.Close)
	socks := freePort(t)
	t.Cleanup(func() { StopV2Ray() })

	// The second and third starts in one process: a metrics server would
	// panic on its reused expvars here.
	for run := 0; run < 3; run++ {
		startCounted(t, socks, site, true)
		if s := statsNow(t); s.Error != "" || s.Outbounds["exit-0"].Downlink != 0 {
			t.Fatalf("run %d before a page: %+v", run, s)
		}
		fetchPage(t, socks)
		s := statsNow(t)
		got := s.Outbounds["exit-0"]
		if s.Error != "" || got.Downlink < pageSize || got.Downlink > 2*pageSize || got.Uplink <= 0 {
			t.Fatalf("run %d after a %d-byte page: %+v", run, pageSize, s)
		}
		if d := s.Outbounds["direct"]; d.Downlink != 0 || d.Uplink != 0 {
			t.Fatalf("run %d: direct counted %+v", run, d)
		}
		if got := StopV2Ray(); got != "SUCCESS" {
			t.Fatalf("stop: %s", got)
		}
	}

	if s := statsNow(t); s.Error != "xray not running" || len(s.Outbounds) != 0 {
		t.Fatalf("stopped core: %+v", s)
	}
}

func TestGetOutboundStatsIsEmptyWithoutCounting(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("z", pageSize))
	}))
	t.Cleanup(site.Close)
	socks := freePort(t)
	t.Cleanup(func() { StopV2Ray() })
	startCounted(t, socks, site, false)
	fetchPage(t, socks)
	if s := statsNow(t); s.Error != "" || len(s.Outbounds) != 0 {
		t.Fatalf("a core that keeps no stats: %+v", s)
	}
}
