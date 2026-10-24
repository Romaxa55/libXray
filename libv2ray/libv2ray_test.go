package libv2ray

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// freePort is a TCP port on 127.0.0.1 nobody listens on.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// config is a SOCKS inbound on 127.0.0.1 and a direct outbound; with geosite
// the routing has a rule that needs geosite.dat.
func config(t *testing.T, geosite bool) string {
	t.Helper()
	rules := []any{}
	if geosite {
		rules = append(rules, map[string]any{
			"type":        "field",
			"domain":      []string{"geosite:category-ru"},
			"outboundTag": "direct",
		})
	}
	out, err := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{
			"tag": "socks-in", "listen": "127.0.0.1", "port": freePort(t), "protocol": "socks",
		}},
		"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}},
		"routing":   map[string]any{"rules": rules},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// datDir is a directory with geosite.dat: LIBXRAY_DAT_DIR, else dat/ of the
// repository, where the builder downloads it.
func datDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("LIBXRAY_DAT_DIR")
	if dir == "" {
		dir = filepath.Join("..", "dat")
	}
	if _, err := os.Stat(filepath.Join(dir, "geosite.dat")); err != nil {
		t.Skipf("no geosite.dat in %s (set LIBXRAY_DAT_DIR)", dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func stats(t *testing.T) memoryStats {
	t.Helper()
	var s memoryStats
	if err := json.Unmarshal([]byte(GetMemoryStats()), &s); err != nil {
		t.Fatalf("GetMemoryStats: %v", err)
	}
	return s
}

func TestMemoryStatsReportTheRuntime(t *testing.T) {
	s := stats(t)
	if s.Running {
		t.Fatal("running before any start")
	}
	if s.Goroutines < 1 || s.Sys == 0 {
		t.Fatalf("empty numbers: %+v", s)
	}
}

func TestStartWithDatDirReadsGeodata(t *testing.T) {
	dir := datDir(t)
	if got := StartV2RayWithConfigAndDatDir(config(t, true), dir); got != "SUCCESS" {
		t.Fatalf("start with %s: %s", dir, got)
	}
	if !stats(t).Running {
		t.Fatal("not running after a start")
	}
	if got := StopV2Ray(); got != "SUCCESS" {
		t.Fatalf("stop: %s", got)
	}
	got := StartV2RayWithConfigAndDatDir(config(t, true), t.TempDir())
	if got == "SUCCESS" {
		StopV2Ray()
		t.Fatal("started without geosite.dat")
	}
	if !strings.Contains(got, "geosite.dat") {
		t.Fatalf("start without geodata failed for another reason: %s", got)
	}
}

func TestStartStopCyclesLeaveNoGoroutines(t *testing.T) {
	// One cycle first: the core's lazily started workers stay for the process.
	if got := StartV2RayWithConfig(config(t, false)); got != "SUCCESS" {
		t.Fatalf("start: %s", got)
	}
	StopV2Ray()
	before := settled(stats(t).Goroutines)
	for i := 0; i < 23; i++ {
		if got := StartV2RayWithConfig(config(t, false)); got != "SUCCESS" {
			t.Fatalf("start %d: %s", i+1, got)
		}
		if got := StopV2Ray(); got != "SUCCESS" {
			t.Fatalf("stop %d: %s", i+1, got)
		}
	}
	if after := settled(before); after > before {
		t.Fatalf("23 start/stop cycles: %d goroutines, %d before", after, before)
	}
}

// settled is the goroutine count once it stops falling below want, or after
// two seconds.
func settled(want int) int {
	deadline := time.Now().Add(2 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= want || time.Now().After(deadline) {
			return n
		}
		time.Sleep(20 * time.Millisecond)
	}
}
