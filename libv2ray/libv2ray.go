// Package libv2ray — Drop-in compatibility shim для старого Libv2ray API.
//
// СОБИРАЕТСЯ ОТДЕЛЬНЫМ XCFRAMEWORK через gomobile bind: получает имя
// Libv2ray.xcframework (по имени Go-package). Все экспорты идут под
// префиксом пакета: Libv2rayInitializeV2Ray, Libv2rayStartV2RayWithConfig
// и т.д. — точно совпадая со старым v2ray-flutter gomobile binding.
//
// Это позволяет MegaV iOS клиенту физически подменить файл
// vpn_native_client/ios/Frameworks/Libv2ray.xcframework на этот новый,
// БЕЗ изменений:
//   - в Swift коде (import Libv2ray + Libv2rayStartV2RayWithConfig работают)
//   - в Runner.xcodeproj (имя framework не меняется)
//   - в pod / code signing (тот же framework name = тот же сертификат)
//
// Под капотом дёргает функции из github.com/xtls/libxray (xray-core v26.3.27),
// который **той же** версии что и backend MegaV_checker rk-checker.
//
// Build (отдельно от LibXray.xcframework):
//   cd libv2ray
//   gomobile bind -target ios,iossimulator,macos,maccatalyst -iosversion 15.0
package libv2ray

import (
	"encoding/base64"
	"encoding/json"
	"runtime"
	"runtime/debug"

	libxray "github.com/xtls/libxray"
	"github.com/xtls/libxray/nodep"
)

// =====================================================================
// Compat shims — names match old Libv2ray gomobile binding 1:1.
// =====================================================================

// InitializeV2Ray — no-op в новом API (state-less).
// Exported as Libv2rayInitializeV2Ray() (gomobile добавляет package prefix).
func InitializeV2Ray() string {
	return "SUCCESS"
}

// StartV2RayWithConfig — упаковывает JSON в base64-request формат
// и вызывает новый RunXrayFromJSON. Возвращает "SUCCESS" или "FAILED: ..."
func StartV2RayWithConfig(configJSON string) string {
	req := libxray.RunXrayFromJSONRequest{
		DatDir:       "", // GeoIP не используем — наш конфиг без geoip:/geosite: rules
		MphCachePath: "",
		ConfigJSON:   configJSON,
	}
	reqBytes, err := json.Marshal(&req)
	if err != nil {
		return "FAILED: marshal request: " + err.Error()
	}
	reqB64 := base64.StdEncoding.EncodeToString(reqBytes)

	respB64 := libxray.RunXrayFromJSON(reqB64)

	respBytes, err := base64.StdEncoding.DecodeString(respB64)
	if err != nil {
		return "FAILED: decode response: " + err.Error()
	}
	var resp nodep.CallResponse[string]
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "FAILED: unmarshal response: " + err.Error()
	}
	if !resp.Success {
		return "FAILED: " + resp.Err
	}
	return "SUCCESS"
}

// StopV2Ray — обёртка над новым StopXray. Идемпотентный: если xray уже
// остановлен, libxray.StopXray() вернёт success=false, err="xray is not
// running" — для нас это OK, возвращаем "SUCCESS" (Swift код ждёт что
// stop idempotent).
func StopV2Ray() string {
	respB64 := libxray.StopXray()
	respBytes, err := base64.StdEncoding.DecodeString(respB64)
	if err != nil {
		return "FAILED: decode response: " + err.Error()
	}
	var resp nodep.CallResponse[string]
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "FAILED: unmarshal response: " + err.Error()
	}
	if !resp.Success {
		// Idempotency: already-stopped не считаем ошибкой.
		if resp.Err == "xray is not running" || resp.Err == "" {
			return "SUCCESS"
		}
		return "FAILED: " + resp.Err
	}
	return "SUCCESS"
}

// IsV2RayRunning — прямой проброс к новому GetXrayState.
func IsV2RayRunning() bool {
	return libxray.GetXrayState()
}

// GetV2RayVersion — возвращает версию xray-core (raw string).
func GetV2RayVersion() string {
	respB64 := libxray.XrayVersion()
	respBytes, err := base64.StdEncoding.DecodeString(respB64)
	if err != nil {
		return "unknown"
	}
	var resp nodep.CallResponse[string]
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "unknown"
	}
	if !resp.Success || resp.Data == "" {
		return "unknown"
	}
	return resp.Data
}

// GetV2RayStatus — старый API возвращал "RUNNING" / "STOPPED".
func GetV2RayStatus() string {
	if libxray.GetXrayState() {
		return "RUNNING"
	}
	return "STOPPED"
}

// CleanupV2Ray — в новом API stateless, явный cleanup не нужен.
func CleanupV2Ray() string {
	return "SUCCESS"
}

// TestV2RayConnection — старый API делал ping через test URL.
// Возвращаем простой "SUCCESS" — реальный ping делает observatory в xray.
func TestV2RayConnection(url string) string {
	if libxray.GetXrayState() {
		return "SUCCESS"
	}
	return "FAILED: xray not running"
}

// ProbeOutbound — honest HTTP-probe через конкретный outbound в работающем
// xray-инстансе. Использует session.SetForcedOutboundTagToContext для
// принудительной маршрутизации (игнорируя balancer/routing).
//
// Returns JSON-строку с полями:
//   outbound_tag, target_url, alive, http_code, rtt_ms, body_excerpt, error, timestamp_ms
//
// Использование Dart (через v2ray_flutter MethodChannel):
//   final r = await V2Ray.probeOutbound(
//     tag: 'server-15',
//     url: 'https://ip.megav.app/',
//     timeoutMs: 5000,
//   );
//   if (r['alive'] && r['http_code'] == 200) {
//     // server-15 жив, exit_ip и asn в body_excerpt
//   }
//
// Memory: ~1.5 MB на active probe — безопасно для iOS NE jetsam 50MB cap.
//
// Подробности реализации: см. xray/probe_outbound.go.
func ProbeOutbound(outboundTag, targetURL string, timeoutMs int) string {
	return libxray.ProbeOutbound(outboundTag, targetURL, timeoutMs)
}

// GetObservatoryState — snapshot текущего состояния burstObservatory:
// alive/dead/RTT для всех outbound'ов из subjectSelector. Внутренний
// доступ к xray-instance, никаких внешних HTTP запросов.
//
// Returns JSON-строку с полями:
//   nodes[]:{tag,alive,delay_ms,ping_all,ping_fail,ping_avg,ping_max,
//            ping_min,ping_deviation,last_error,last_seen_ms,last_try_ms}
//   timestamp_ms
//   error (если что-то пошло не так)
//
// Winner balancer'а Dart считает сам: min(delay_ms) среди alive=true.
//
// Использование Dart (через v2ray_flutter MethodChannel):
//   final s = await V2Ray.getObservatoryState();
//   for (final n in s['nodes']) {
//     debugPrint('${n['tag']}: alive=${n['alive']} rtt=${n['delay_ms']}ms');
//   }
//
// requestJSON — зарезервирован под будущие расширения (фильтрация по
// тегам, например). Сейчас можно передавать "" — игнорируется.
//
// Подробности реализации: см. xray/observatory_state.go.
func GetObservatoryState(requestJSON string) string {
	return libxray.GetObservatoryState(requestJSON)
}

// GetBuildInfo — JSON с метаданными собранной libv2ray (gomobile wrapper):
// xray_version, go_version, libxray_commit (если VCS-stamping есть),
// features (карта feature-flags). Главный флаг — pr5805_balancer_dialer.
//
// Dart usage:
//   final info = await V2Ray.getBuildInfo();
//   if (info['features']['pr5805_balancer_dialer'] != true) {
//     // upstream xray без форка — chain-mode не работает,
//     // переключаемся на simple-chain через single outbound.
//   }
//
// См. libXray/xray/build_info.go — там полное объяснение detection-логики.
func GetBuildInfo() string {
	return libxray.GetBuildInfo()
}

// CheckVersionX — устаревший alias для XrayVersion.
func CheckVersionX() string {
	return GetV2RayVersion()
}

// ForceGC — старый custom helper в Libv2ray для memory pressure.
// libXray сам делает периодический GC (см. memory_ios.go тик 1сек).
// Этот вызов — explicit trigger когда iOS NE получил memory warning.
//
// В новом libxray API нет exported ForceGC, поэтому делаем напрямую
// через runtime/debug — это нормально для in-process Go-кода.
func ForceGC() string {
	debug.FreeOSMemory()
	runtime.GC()
	return "SUCCESS"
}

// GetMemoryStats — старый custom helper для diagnostics.
func GetMemoryStats() string {
	if libxray.GetXrayState() {
		return `{"compat":"stub","running":true}`
	}
	return `{"compat":"stub","running":false}`
}

// ConvertUrlToConfig — нативный xray-парсер URL → JSON xray-config.
//
// Зачем: убирает огромный куст Dart-парсера (V2RayUrlParser, ~600 строк +
// V2RayOutboundGenerator, ~400 строк), который для каждого нового xray-фичи
// надо вручную портировать. Здесь xray сам парсит свой URL и возвращает
// готовый conf.Config со всеми streamSettings, tlsSettings, realitySettings,
// sockopt, fingerprint, alpn, и т.д. — что бы там ни добавил xtls upstream.
//
// Input:  plain URL string ("vless://...?...", "vmess://...", "trojan://...",
//         "ss://...", или v2rayN base64-bundle, или Clash YAML)
// Output: plain JSON string с полной xray Config (с inbounds/outbounds/etc.)
//         либо "FAILED: <reason>" при ошибке парсинга.
//
// На Dart-стороне разбирать вернувшийся JSON и брать только outbounds[0]
// (proxy outbound). Inbounds/routing/dns мобила собирает сама.
//
// Под капотом дёргает libxray.ConvertShareLinksToXrayJson (base64-envelope),
// здесь делаем удобную plain-string обёртку чтобы Dart-сторона не возилась
// с base64.
func ConvertUrlToConfig(url string) string {
	reqB64 := base64.StdEncoding.EncodeToString([]byte(url))
	respB64 := libxray.ConvertShareLinksToXrayJson(reqB64)

	respBytes, err := base64.StdEncoding.DecodeString(respB64)
	if err != nil {
		return "FAILED: decode response: " + err.Error()
	}

	// nodep.CallResponse[*conf.Config] — но conf.Config мы здесь не
	// импортируем (gomobile не любит сложные struct'ы из xray-core).
	// Парсим как generic JSON и достаём Data поле напрямую.
	var resp struct {
		Success bool            `json:"success"`
		Err     string          `json:"err,omitempty"`
		Data    json.RawMessage `json:"data,omitempty"`
	}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return "FAILED: unmarshal response: " + err.Error()
	}
	if !resp.Success {
		return "FAILED: " + resp.Err
	}
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		return "FAILED: empty config returned for url"
	}
	// resp.Data это уже валидный JSON конфига xray.
	return string(resp.Data)
}
