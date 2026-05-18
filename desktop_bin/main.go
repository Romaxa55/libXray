package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xtls/libxray/xray"
)

func main() {
	// Wrapper-конфиг (старый путь, для tun-сборок desktop):
	configPath := flag.String("configPath", "", "Path of wrapper config.json (with datDir, mphCachePath, configPath)")
	// Прямой xray-config (новый удобный путь для lab-тестов):
	xrayConfig := flag.String("xrayConfig", "", "Path to raw xray-config JSON (skips wrapper, no tun, no DNS)")
	// Probe-режим (после старта xray делает probe выбранных outbounds и выходит):
	probeTags := flag.String("probeTags", "", "Comma-separated outbound tags для honest-probe (например 'server-0,server-1'). Spec 'ALL' = из burstObservatory.subjectSelector.")
	probeURL := flag.String("probeUrl", "https://ip.megav.app/", "Target URL для probe")
	probeTimeout := flag.Int("probeTimeoutMs", 5000, "Probe timeout per outbound, ms")
	probeConcurrency := flag.Int("probeConcurrency", 5, "Сколько параллельных probe запускать (5 норм для iOS 50MB cap)")
	keepRunning := flag.Bool("keepRunning", false, "После probe не выходить (для curl-тестов вручную)")
	flag.Parse()

	// 1. Старт xray по одному из двух путей
	var startErr error
	if *xrayConfig != "" {
		startErr = runXrayDirect(*xrayConfig)
	} else if *configPath != "" {
		startErr = runXray(*configPath)
	} else {
		fmt.Fprintln(os.Stderr, "ERROR: укажи либо --configPath, либо --xrayConfig")
		flag.Usage()
		os.Exit(2)
	}
	if startErr != nil {
		fmt.Fprintf(os.Stderr, "xray start failed: %v\n", startErr)
		os.Exit(1)
	}
	defer stopXray()

	runtime.GC()
	debug.FreeOSMemory()

	// 2. Если задан --probeTags — гоним honest-probe и выходим (если не --keepRunning)
	if *probeTags != "" {
		// Даём xray 1.5s полностью запуститься (listening sockets, observatory init)
		time.Sleep(1500 * time.Millisecond)

		tags := resolveProbeTags(*probeTags, *xrayConfig)
		fmt.Fprintf(os.Stderr, "[probe] starting honest-probe: %d tags, url=%s, timeout=%dms, concurrency=%d\n",
			len(tags), *probeURL, *probeTimeout, *probeConcurrency)

		results := runProbeBatch(tags, *probeURL, *probeTimeout, *probeConcurrency)

		// Один JSON-array на stdout — удобно парсить
		fmt.Println(results)

		if !*keepRunning {
			return
		}
	}

	// 3. Иначе ждём SIGINT/SIGTERM (стандартный режим)
	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM)
	<-osSignals
}

// runXrayDirect запускает xray с raw config-файлом, без wrapper-структуры.
// Удобно для lab-тестов на Mac (мобильный config один-в-один работает).
func runXrayDirect(xrayConfigPath string) error {
	configBytes, err := os.ReadFile(xrayConfigPath)
	if err != nil {
		return fmt.Errorf("read xray config: %w", err)
	}
	// Полный путь к директории конфига как datDir (для geoip.dat/geosite.dat fallback)
	return xray.RunXrayFromJSON("", "", string(configBytes))
}

// resolveProbeTags — если задано "ALL", парсим burstObservatory.subjectSelector
// из xray-config файла (надо явно знать какие outbound включены). Если задан
// явный список через запятую — используем его.
func resolveProbeTags(spec, xrayConfigPath string) []string {
	if spec != "ALL" {
		var out []string
		for _, t := range strings.Split(spec, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	// spec == "ALL" → читаем burstObservatory.subjectSelector
	if xrayConfigPath == "" {
		fmt.Fprintln(os.Stderr, "[probe] ALL spec требует --xrayConfig (для парсинга subjectSelector)")
		return nil
	}
	data, err := os.ReadFile(xrayConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[probe] read config: %v\n", err)
		return nil
	}
	var cfg struct {
		BurstObservatory struct {
			SubjectSelector []string `json:"subjectSelector"`
		} `json:"burstObservatory"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[probe] parse config: %v\n", err)
		return nil
	}
	return cfg.BurstObservatory.SubjectSelector
}

// runProbeBatch — параллельный probe нескольких outbound, концурренция
// ограничена semaphore, чтобы пик памяти не вылез за iOS jetsam 50MB.
// Возвращает JSON-массив с результатами для удобного парсинга в shell/CI.
func runProbeBatch(tags []string, url string, timeoutMs, concurrency int) string {
	if concurrency <= 0 {
		concurrency = 5
	}
	results := make([]json.RawMessage, len(tags))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, tag := range tags {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, tag string) {
			defer wg.Done()
			defer func() { <-sem }()
			r := xray.ProbeOutbound(tag, url, timeoutMs)
			results[i] = json.RawMessage(r)
			fmt.Fprintf(os.Stderr, "[probe] %s done\n", tag)
		}(i, tag)
	}
	wg.Wait()

	out, _ := json.MarshalIndent(results, "", "  ")
	return string(out)
}
