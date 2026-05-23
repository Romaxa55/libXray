package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof" // 2026-05-22: pprof memory-analyzer (desktop only)
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xtls/libxray/share"
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
	convertUrl := flag.String("convertUrl", "", "Конвертирует share-URL (vless://, vmess://, trojan://, ss://) в xray-config JSON через нативный парсер libxray. Печатает в stdout и выходит.")
	// Observatory state режим: ждёт N сек после старта (чтобы observatory сделал
	// несколько раундов probe), потом вызывает GetObservatoryState и печатает JSON.
	// Используется для lab-проверки нового API до сборки libXray для мобилок.
	observatoryState := flag.Bool("observatoryState", false, "После старта подождать --observatoryWait сек и напечатать ObservatoryState JSON")
	observatoryWait := flag.Int("observatoryWait", 35, "Сколько секунд ждать observatory'у на раунды probe перед snapshot'ом (default 35 = чуть больше observatoryInterval=30s)")
	observatoryPoll := flag.Int("observatoryPoll", 0, "Если >0 — печатать ObservatoryState каждые N сек (стрим, для отладки live-обновления). 0 = single shot.")
	// 2026-05-22 (юзер): pprof HTTP endpoint для memory profiler.
	// Использование: ./xray-megav-pprof --xrayConfig=... --keepRunning --pprofPort=6060
	// Затем: go tool pprof http://localhost:6060/debug/pprof/heap
	pprofPort := flag.Int("pprofPort", 0, "Если >0 — запустить pprof HTTP server на localhost:N (memory analyzer). 0 = выключено.")
	flag.Parse()

	if *pprofPort > 0 {
		go func() {
			addr := fmt.Sprintf("localhost:%d", *pprofPort)
			fmt.Fprintf(os.Stderr, "[pprof] HTTP server: http://%s/debug/pprof/\n", addr)
			fmt.Fprintf(os.Stderr, "[pprof] heap:       go tool pprof http://%s/debug/pprof/heap\n", addr)
			fmt.Fprintf(os.Stderr, "[pprof] goroutines: go tool pprof http://%s/debug/pprof/goroutine\n", addr)
			fmt.Fprintf(os.Stderr, "[pprof] stats raw:  curl http://%s/debug/vars\n", addr)
			if err := http.ListenAndServe(addr, nil); err != nil {
				fmt.Fprintf(os.Stderr, "[pprof] server error: %v\n", err)
			}
		}()
	}

	// 0. --convertUrl: одноразовая конвертация URL → JSON через native parser.
	// Полезно для дебага: проверить что Go-парсер реально кладёт в
	// streamSettings.tlsSettings.allowInsecure / serverName / etc.
	if *convertUrl != "" {
		cfg, err := share.ConvertShareLinksToXrayJson(*convertUrl)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAILED: %v\n", err)
			os.Exit(1)
		}
		out, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAILED to marshal: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
		return
	}

	// 1. Старт xray по одному из двух путей
	var startErr error
	if *xrayConfig != "" {
		startErr = runXrayDirect(*xrayConfig)
	} else if *configPath != "" {
		startErr = runXray(*configPath)
	} else {
		fmt.Fprintln(os.Stderr, "ERROR: укажи либо --configPath, либо --xrayConfig, либо --convertUrl")
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

	// 2.5 Observatory-state режим. Ждём N сек чтобы burstObservatory успел
	// сделать хотя бы пару раундов (observatoryInterval=30s, sampling=3 →
	// ~90s для полной картины). На 35s обычно уже видны первые delay'и.
	//
	// Полезно для lab-теста: реальный xray-config, observatory работает,
	// смотрим что отдаёт GetObservatoryState — JSON со всеми узлами и их
	// alive/RTT. Прежде чем собирать libXray для мобилок.
	if *observatoryState {
		fmt.Fprintf(os.Stderr, "[obs-state] waiting %d sec for observatory rounds...\n", *observatoryWait)
		time.Sleep(time.Duration(*observatoryWait) * time.Second)

		if *observatoryPoll > 0 {
			// Live-стрим: печатаем snapshot каждые N сек. Удобно смотреть как
			// узлы переходят alive/dead, как delay колеблется во времени.
			fmt.Fprintf(os.Stderr, "[obs-state] streaming every %ds, press Ctrl-C to stop\n", *observatoryPoll)
			osSignals := make(chan os.Signal, 1)
			signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM)
			ticker := time.NewTicker(time.Duration(*observatoryPoll) * time.Second)
			defer ticker.Stop()
			// Первый snapshot — сразу
			printObservatorySnapshot()
			for {
				select {
				case <-ticker.C:
					printObservatorySnapshot()
				case <-osSignals:
					return
				}
			}
		}

		// Single-shot
		printObservatorySnapshot()
		if !*keepRunning {
			return
		}
	}

	// 3. Иначе ждём SIGINT/SIGTERM (стандартный режим)
	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM)
	<-osSignals
}

// printObservatorySnapshot вызывает xray.GetObservatoryState и красиво
// печатает JSON-результат. Для отдельной строки времени префиксуем
// нашим timestamp'ом — чтоб в стриме различать снимки.
func printObservatorySnapshot() {
	raw := xray.GetObservatoryState("")
	// Pretty-print: парсим обратно и MarshalIndent — JSON станет читаемым,
	// удобно глазом проверять.
	var parsed interface{}
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		out, _ := json.MarshalIndent(parsed, "", "  ")
		fmt.Printf("---- obs-state @ %s ----\n%s\n",
			time.Now().Format("15:04:05"), string(out))
	} else {
		// raw был не-JSON по какой-то причине — печатаем как есть
		fmt.Printf("---- obs-state @ %s (raw, parse failed: %v) ----\n%s\n",
			time.Now().Format("15:04:05"), err, raw)
	}
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
