// Package main — cgo c-archive compat-shim для macOS/desktop клиентов.
//
// СОБИРАЕТСЯ как `c-archive` через `go build -buildmode=c-archive` —
// результат: `libv2ray.a` + `libv2ray.h` с plain C-API экспортами.
// Это используется macOS клиентом v2ray_flutter (не gomobile bind!).
//
// Старый API контракт (что ждёт V2RayWrapper.m на macOS):
//
//   extern char* InitializeV2Ray(void);
//   extern char* StartV2RayWithConfig(char* configJSON);
//   extern char* StopV2Ray(void);
//   extern int   IsV2RayRunning(void);
//   extern char* GetV2RayVersion(void);
//   extern char* TestV2RayConnection(char* url);
//   extern char* GetV2RayStatus(void);
//   extern char* CleanupV2Ray(void);
//   extern void  Free(char* ptr);
//
// Под капотом — те же compat-helpers что и для gomobile-binding (libv2ray/),
// просто экспонируются через cgo для C-linkage.
//
// Build:
//   # arm64
//   CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
//     go build -buildmode=c-archive \
//     -o build/macos-arm64/libv2ray.a \
//     ./libv2ray_cgo
//   # x86_64
//   CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
//     go build -buildmode=c-archive \
//     -o build/macos-amd64/libv2ray.a \
//     ./libv2ray_cgo
//   # Universal lipo
//   lipo -create build/macos-arm64/libv2ray.a build/macos-amd64/libv2ray.a \
//     -output build/libv2ray.a
//
// IMPORTANT: package MUST be "main" for c-archive buildmode.
package main

// #include <stdlib.h>
import "C"

import (
	"encoding/base64"
	"encoding/json"
	"runtime"
	"runtime/debug"
	"unsafe"

	libxray "github.com/xtls/libxray"
	"github.com/xtls/libxray/nodep"
)

// Required for c-archive — пустой main, реальная инициализация Go runtime
// произойдёт при первом cgo-вызове.
func main() {}

// =====================================================================
// cgo exports — точно соответствуют старому macOS libv2ray.h API.
// =====================================================================

//export InitializeV2Ray
func InitializeV2Ray() *C.char {
	// No-op в новом API (stateless).
	return C.CString("SUCCESS")
}

//export StartV2RayWithConfig
func StartV2RayWithConfig(configJSON *C.char) *C.char {
	cfg := C.GoString(configJSON)
	req := libxray.RunXrayFromJSONRequest{
		DatDir:       "", // GeoIP не используем — конфиг без geoip:/geosite: rules
		MphCachePath: "",
		ConfigJSON:   cfg,
	}
	reqBytes, err := json.Marshal(&req)
	if err != nil {
		return C.CString("FAILED: marshal request: " + err.Error())
	}
	reqB64 := base64.StdEncoding.EncodeToString(reqBytes)

	respB64 := libxray.RunXrayFromJSON(reqB64)

	respBytes, err := base64.StdEncoding.DecodeString(respB64)
	if err != nil {
		return C.CString("FAILED: decode response: " + err.Error())
	}
	var resp nodep.CallResponse[string]
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return C.CString("FAILED: unmarshal response: " + err.Error())
	}
	if !resp.Success {
		return C.CString("FAILED: " + resp.Err)
	}
	return C.CString("SUCCESS")
}

//export StopV2Ray
func StopV2Ray() *C.char {
	respB64 := libxray.StopXray()
	respBytes, err := base64.StdEncoding.DecodeString(respB64)
	if err != nil {
		return C.CString("FAILED: decode response: " + err.Error())
	}
	var resp nodep.CallResponse[string]
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return C.CString("FAILED: unmarshal response: " + err.Error())
	}
	if !resp.Success {
		// Idempotency: already-stopped не считаем ошибкой.
		if resp.Err == "xray is not running" || resp.Err == "" {
			return C.CString("SUCCESS")
		}
		return C.CString("FAILED: " + resp.Err)
	}
	return C.CString("SUCCESS")
}

//export IsV2RayRunning
func IsV2RayRunning() C.int {
	if libxray.GetXrayState() {
		return 1
	}
	return 0
}

//export GetV2RayVersion
func GetV2RayVersion() *C.char {
	respB64 := libxray.XrayVersion()
	respBytes, err := base64.StdEncoding.DecodeString(respB64)
	if err != nil {
		return C.CString("unknown")
	}
	var resp nodep.CallResponse[string]
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return C.CString("unknown")
	}
	if !resp.Success || resp.Data == "" {
		return C.CString("unknown")
	}
	return C.CString(resp.Data)
}

//export TestV2RayConnection
func TestV2RayConnection(url *C.char) *C.char {
	_ = url // ping URL not used — observatory делает ping внутри
	if libxray.GetXrayState() {
		return C.CString("SUCCESS")
	}
	return C.CString("FAILED: xray not running")
}

// ProbeOutbound — honest HTTP-probe через указанный outbound в работающем
// xray-инстансе. Принудительная маршрутизация через session.SetForcedOutboundTagToContext.
//
// Returns C-string JSON. Caller MUST free через Free(ptr).
//
// Использование из Swift/Obj-C на macOS:
//   let raw = ProbeOutbound(strdup("server-15"), strdup("https://ip.megav.app/"), 5000)
//   let json = String(cString: raw!)
//   Free(raw)
//
// См. libv2ray/libv2ray.go::ProbeOutbound — документация полная.
//
//export ProbeOutbound
func ProbeOutbound(outboundTag *C.char, targetURL *C.char, timeoutMs C.int) *C.char {
	tag := C.GoString(outboundTag)
	url := C.GoString(targetURL)
	timeout := int(timeoutMs)
	return C.CString(libxray.ProbeOutbound(tag, url, timeout))
}

// GetObservatoryState — snapshot текущего состояния burstObservatory.
// Internal-only вызов (без сети) — observatory сама пингует в фоне.
//
// Returns C-string JSON. Caller MUST free через Free(ptr).
//
// Использование из Swift/Obj-C на macOS:
//   let raw = GetObservatoryState(strdup(""))  // requestJSON пока не используется
//   let json = String(cString: raw!)
//   Free(raw)
//   // parse JSON: {"nodes":[{"tag":"bs-0","alive":true,"delay_ms":818,...}], ...}
//
// См. libv2ray/libv2ray.go::GetObservatoryState — полная документация.
//
//export GetObservatoryState
func GetObservatoryState(requestJSON *C.char) *C.char {
	req := C.GoString(requestJSON)
	return C.CString(libxray.GetObservatoryState(req))
}

// GetBuildInfo — метаданные собранной libv2ray.a (cgo wrapper):
// xray_version, go_version, features (PR #5805, observatory_state, etc.).
//
// Returns C-string JSON. Caller MUST free через Free(ptr).
//
// См. libv2ray/libv2ray.go::GetBuildInfo и libXray/xray/build_info.go.
//
//export GetBuildInfo
func GetBuildInfo() *C.char {
	return C.CString(libxray.GetBuildInfo())
}

//export GetV2RayStatus
func GetV2RayStatus() *C.char {
	if libxray.GetXrayState() {
		return C.CString("RUNNING")
	}
	return C.CString("STOPPED")
}

//export CleanupV2Ray
func CleanupV2Ray() *C.char {
	// No-op в новом API (stateless).
	return C.CString("SUCCESS")
}

//export Free
func Free(ptr *C.char) {
	C.free(unsafe.Pointer(ptr))
}

// Bonus — для будущего memory pressure handling из Swift.
//
//export ForceGC
func ForceGC() *C.char {
	debug.FreeOSMemory()
	runtime.GC()
	return C.CString("SUCCESS")
}

// ConvertUrlToConfig — нативный xray-парсер URL → JSON xray-config.
// См. libv2ray/libv2ray.go ConvertUrlToConfig для документации.
//
//export ConvertUrlToConfig
func ConvertUrlToConfig(url *C.char) *C.char {
	u := C.GoString(url)
	reqB64 := base64.StdEncoding.EncodeToString([]byte(u))
	respB64 := libxray.ConvertShareLinksToXrayJson(reqB64)

	respBytes, err := base64.StdEncoding.DecodeString(respB64)
	if err != nil {
		return C.CString("FAILED: decode response: " + err.Error())
	}
	var resp struct {
		Success bool            `json:"success"`
		Err     string          `json:"err,omitempty"`
		Data    json.RawMessage `json:"data,omitempty"`
	}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return C.CString("FAILED: unmarshal response: " + err.Error())
	}
	if !resp.Success {
		return C.CString("FAILED: " + resp.Err)
	}
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		return C.CString("FAILED: empty config returned for url")
	}
	return C.CString(string(resp.Data))
}
