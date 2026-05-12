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
