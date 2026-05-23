//go:build pprof_enabled
// +build pprof_enabled

// Package libv2ray — pprof HTTP endpoint для memory profiling на iOS/macOS.
//
// 2026-05-23 (юзер): iOS NE (MegaVTunnel) показывает 77-82 МБ RSS при
// 50 МБ jetsam cap. Xcode Memory Graph и Instruments либо отказываются
// attach'ся к Network Extension (security restrictions), либо дают
// искажённую картину из-за MallocStackLogging overhead.
//
// Решение: вкомпилить net/http/pprof напрямую в Libv2ray.xcframework
// под build tag `pprof_enabled`. Релизные сборки для App Store этот файл
// НЕ включают (tag не задан → срабатывает pprof_stub.go). Debug-сборки
// получают endpoint http://localhost:<port>/debug/pprof/.
//
// Использование с iPhone (с Mac через USB-туннель):
//
//   # Mac (один раз):
//   brew install libimobiledevice
//
//   # Mac, во время работы NE:
//   iproxy 6060 6060
//
//   # Mac, в другом окне:
//   go tool pprof http://localhost:6060/debug/pprof/heap
//   (top, list <func>, web)
//
// Build:
//   PATH="$HOME/go/bin:$PATH" LIBV2RAY_PPROF=1 \
//     python3 build/main.py apple all
//
// (env-var LIBV2RAY_PPROF=1 заставляет apple_gomobile.py передать
//  `-tags pprof_enabled` в gomobile bind).
package libv2ray

import (
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof" // регистрирует /debug/pprof/* handlers в DefaultServeMux
	"runtime"
	"sync"
)

var (
	pprofMu       sync.Mutex
	pprofListener net.Listener
	pprofRunning  bool
)

// StartPprof запускает HTTP-сервер с pprof endpoint'ом на localhost:port.
//
// На iOS Network Extension работает в отдельном процессе, сетевой
// listener в нём ограничен sandbox'ом — но localhost loopback РАЗРЕШЁН,
// т.к. он не нарушает entitlement'ы. Для доступа с Mac используй iproxy
// чтобы пробросить порт через USB (libimobiledevice).
//
// Возвращает JSON со статусом:
//
//	{"ok":true,"port":6060,"url":"http://localhost:6060/debug/pprof/"}
//	{"ok":false,"error":"..."}
//
// Двойной вызов безопасен — второй вернёт already_running.
func StartPprof(port int) string {
	pprofMu.Lock()
	defer pprofMu.Unlock()

	if pprofRunning {
		return fmt.Sprintf(`{"ok":true,"port":%d,"status":"already_running"}`, port)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"error":%q}`, err.Error())
	}
	pprofListener = ln
	pprofRunning = true

	// Прогреем GC, чтобы первый heap snapshot был релевантным.
	runtime.GC()

	go func() {
		// Используем DefaultServeMux — туда уже зарегистрированы
		// pprof handlers через _ "net/http/pprof" импорт.
		_ = http.Serve(ln, nil)
		pprofMu.Lock()
		pprofRunning = false
		pprofMu.Unlock()
	}()

	return fmt.Sprintf(
		`{"ok":true,"port":%d,"url":"http://localhost:%d/debug/pprof/","build_tag":"pprof_enabled"}`,
		port, port,
	)
}

// StopPprof закрывает listener. После этого pprof endpoint становится
// недоступен — иначе он живёт до завершения процесса NE.
func StopPprof() string {
	pprofMu.Lock()
	defer pprofMu.Unlock()

	if !pprofRunning || pprofListener == nil {
		return `{"ok":true,"status":"not_running"}`
	}

	if err := pprofListener.Close(); err != nil {
		return fmt.Sprintf(`{"ok":false,"error":%q}`, err.Error())
	}
	pprofListener = nil
	pprofRunning = false
	return `{"ok":true,"status":"stopped"}`
}

// PprofEnabled — флаг для Swift-стороны чтобы понять, есть ли pprof в
// текущем билде Libv2ray.xcframework. Если StartPprof вернёт error
// "not_compiled" — pprof отключён, не стоит звать.
func PprofEnabled() bool {
	return true
}
