package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	xraynet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
)

// ProbeOutboundResult — структура результата probe одного outbound'а.
type ProbeOutboundResult struct {
	OutboundTag string `json:"outbound_tag"`
	TargetURL   string `json:"target_url"`
	Alive       bool   `json:"alive"`
	HTTPCode    int    `json:"http_code,omitempty"`
	RttMs       int64  `json:"rtt_ms"`
	BodyExcerpt string `json:"body_excerpt,omitempty"`
	Error       string `json:"error,omitempty"`
	TimestampMs int64  `json:"timestamp_ms"`
}

// probeSemaphore ограничивает количество одновременных probe-ов внутри
// одного процесса. Защищает от iOS NE jetsam 50MB cap — каждый active probe
// держит ~1.5MB TLS-session, 5 параллельно = +7.5MB пик, безопасно.
//
// Если caller (Dart) запустит 20 probe через Future.wait — semaphore
// сериализует их пачками по 5. Чуть медленнее (4 раунда × ~1s = ~4s),
// но без OOM-kill экстеншна.
var probeSemaphore = make(chan struct{}, 5)

// loadCoreServer возвращает текущий running xray-instance ATOMICALLY.
//
// Зачем atomic: без него RunXrayFromJSON/StopXray могут изменить глобал
// coreServer пока probe-горутины в полёте — classic data race + nil-deref.
// Особенно опасно на StopXray: проверка != nil проходит, потом другая
// горутина зануляет, потом dial панически крашится. На iOS NE это
// killswitch для пользователя.
//
// Используем существующий coreServer из xray.go через type-assertion
// — мы один package, импорт не нужен.
func loadCoreServer() *core.Instance {
	v := coreServerPtr.Load()
	if v == nil {
		return nil
	}
	return v
}

// coreServerPtr — atomic-обёртка над глобальной переменной coreServer
// из xray.go. Инициализируется через init() при первом обращении и
// синхронизируется с обычным coreServer (наследие).
//
// КОНТРАКТ: каждый sync с глобалом (StartXray/StopXray/etc) должен
// вызвать syncCoreServerPtr() сразу после изменения coreServer.
var (
	coreServerPtr atomic.Pointer[core.Instance]
	syncOnce      sync.Once
)

func syncCoreServerPtr() {
	coreServerPtr.Store(coreServer)
}

// ProbeOutbound делает honest HTTP-probe через конкретный outbound в
// запущенном xray-инстансе. Возвращает JSON со статусом, RTT, кодом ответа
// и первыми 1024 байтами body (для верификации что мы реально вышли через
// нужный exit — например в body от ip.megav.app будет наш exit_ip и asn).
//
// Параметры:
//
//	outboundTag — tag нужного outbound (например "server-15", "ru-bootstrap")
//	targetURL   — полный URL для GET-запроса (например "https://ip.megav.app/")
//	timeoutMs   — общий таймаут (TCP+TLS+HTTP), миллисекунды (5000 = норма)
//
// Особенности:
//   - Использует core.Dial(ctx, instance, dest) — стандартный xray-core API,
//     известный из xray-knife/MeasureDelay.
//   - Через session.SetForcedOutboundTagToContext + core.toContext (вшито
//     в core.Dial) принудительно роутит трафик в указанный outboundTag,
//     ИГНОРИРУЯ balancer/routing rules.
//   - Внутренний semaphore (5 паралл.) защищает iOS NE jetsam 50MB cap.
//   - atomic.Pointer на coreServer — безопасно при concurrent Stop/Probe.
//   - НЕ требует extra inbound портов. Всё внутри одного процесса.
//
// Использование (Dart):
//
//	final r = await V2Ray.probeOutbound(tag: 'server-15', url: '...', timeoutMs: 5000);
//	if (r['alive']) { ... }
//
// Использование (CLI desktop_bin для теста):
//
//	xray-megav --probe-outbound=server-15 --probe-url=https://ip.megav.app/
func ProbeOutbound(outboundTag, targetURL string, timeoutMs int) string {
	startMs := time.Now().UnixMilli()
	result := ProbeOutboundResult{
		OutboundTag: outboundTag,
		TargetURL:   targetURL,
		TimestampMs: startMs,
	}

	// Sanity-clamp timeout: [100ms, 60s]. 0/negative → 5s default.
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeoutMs <= 0 {
		timeout = 5 * time.Second
	} else if timeoutMs < 100 {
		timeout = 100 * time.Millisecond
	} else if timeoutMs > 60_000 {
		timeout = 60 * time.Second
	}

	// 0. P1-5: URL scheme whitelist.
	//    Защита от Firebase Remote Config / malformed remote URL подсунувших
	//    file:///etc/passwd, javascript:alert(1), http://localhost:5555/admin
	//    и подобных угроз. Принимаем ТОЛЬКО https/http.
	parsedURL, urlErr := url.Parse(targetURL)
	if urlErr != nil {
		result.Error = fmt.Sprintf("invalid URL: %v", urlErr)
		return marshalResult(result)
	}
	if parsedURL.Scheme != "https" && parsedURL.Scheme != "http" {
		result.Error = fmt.Sprintf("URL scheme %q not allowed (only http/https)", parsedURL.Scheme)
		return marshalResult(result)
	}
	if parsedURL.Host == "" {
		result.Error = "URL host is empty"
		return marshalResult(result)
	}

	// 1. Snapshot xray instance через atomic load.
	//    Защита от concurrent Stop — даже если StopXray зануляет coreServer
	//    после нашего load, мы продолжаем работать с локальным указателем
	//    и instance корректно отработает Close в графе через GC.
	syncOnce.Do(syncCoreServerPtr)
	inst := loadCoreServer()
	if inst == nil || !inst.IsRunning() {
		result.Error = "xray instance not running"
		return marshalResult(result)
	}

	// 2. Acquire semaphore (max 5 concurrent probes).
	//    Используем context.WithTimeout вместо time.After — иначе
	//    оставшиеся таймеры висят в куче до истечения, что на нагрузке
	//    "20 probes × минута" даёт ~28800 dangling timers/день и копит
	//    GC pressure (P1-1 в review).
	acquireCtx, cancelAcquire := context.WithTimeout(context.Background(), timeout)
	defer cancelAcquire()
	select {
	case probeSemaphore <- struct{}{}:
		defer func() { <-probeSemaphore }()
	case <-acquireCtx.Done():
		result.RttMs = time.Since(time.UnixMilli(startMs)).Milliseconds()
		result.Error = "semaphore timeout (5 concurrent probes max)"
		return marshalResult(result)
	}

	// 3. HTTP-клиент с DialContext который route'ит ВСЁ через указанный outbound.
	//    Ключевой механизм:
	//      - core.Dial(ctx, inst, dest) — стандартный API xray-core,
	//        внутри делает toContext(ctx, inst) → dispatcher.Dispatch(ctx, dest).
	//        Таким образом freedom/DNS-резолв в downstream-handlers получит
	//        instance из ctx и не упадёт с "Instance context variable is not in context".
	//      - session.SetForcedOutboundTagToContext помещает tag в ctx до Dial,
	//        dispatcher видит этот forced tag первым делом и ВСЕГДА роутит туда,
	//        игнорируя balancer/routing rules. См. xray-core/app/dispatcher/default.go:457.
	//
	//    P2-2: Content создаём один раз и явно (SkipDNSResolve+forced-tag вместе).
	//    SetForcedOutboundTagToContext сам бы создал Content если его нет, но это
	//    хрупкий ordering — здесь явно гарантируем что SkipDNSResolve не потеряется.
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dest, err := xraynet.ParseDestination(fmt.Sprintf("%s:%s", network, addr))
			if err != nil {
				return nil, err
			}
			ctx = session.ContextWithContent(ctx, &session.Content{SkipDNSResolve: true})
			ctx = session.SetForcedOutboundTagToContext(ctx, outboundTag)
			// core.Dial сам сделает toContext(ctx, inst) внутри.
			return core.Dial(ctx, inst, dest)
		},
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	// 4. HTTP GET
	dialStart := time.Now()
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		result.RttMs = time.Since(dialStart).Milliseconds()
		result.Error = fmt.Sprintf("build request: %v", err)
		return marshalResult(result)
	}
	req.Header.Set("User-Agent", "MegaV-ProbeOutbound/1.0")

	resp, err := client.Do(req)
	result.RttMs = time.Since(dialStart).Milliseconds()

	if err != nil {
		result.Error = trimError(err.Error(), 256)
		return marshalResult(result)
	}
	defer resp.Body.Close()

	// 5. Парсим ответ. Берём первые 1024 байта body — хватает чтобы распарсить
	//    JSON от ip.megav.app со всеми полями (asn_organization может быть
	//    длинным: "Sovremennye setevye tekhnologii" = 30 chars).
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	// 6. Guard против невалидного UTF-8: если body обрезался посередине
	//    мульти-байтовой руны (например кириллицы в asn_org), json.Marshal
	//    выдаст невалидный JSON, и Dart jsonDecode упадёт FormatException.
	if !utf8.Valid(bodyBytes) {
		bodyBytes = bytes.ToValidUTF8(bodyBytes, []byte{0xEF, 0xBF, 0xBD}) // U+FFFD replacement
	}

	result.HTTPCode = resp.StatusCode
	result.BodyExcerpt = string(bodyBytes)
	result.Alive = resp.StatusCode >= 200 && resp.StatusCode < 400

	return marshalResult(result)
}

func marshalResult(r ProbeOutboundResult) string {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf(`{"outbound_tag":%q,"alive":false,"error":"marshal failed: %s"}`,
			r.OutboundTag, err.Error())
	}
	return string(b)
}

func trimError(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
