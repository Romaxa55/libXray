//go:build !pprof_enabled
// +build !pprof_enabled

// Package libv2ray — pprof STUB для release-сборок.
//
// Если build tag `pprof_enabled` НЕ задан (релиз для App Store),
// pprof.go выкидывается из сборки. Этот файл предоставляет no-op
// заглушки чтобы gomobile bind генерил один и тот же API surface
// (Libv2rayStartPprof, Libv2rayStopPprof, Libv2rayPprofEnabled)
// независимо от наличия tag'а.
//
// Это важно: Swift NE компилируется один раз, а Libv2ray.xcframework
// можно подменить debug↔release без перекомпиляции NE.
package libv2ray

// StartPprof в релизе ничего не делает.
func StartPprof(port int) string {
	return `{"ok":false,"error":"not_compiled","hint":"rebuild Libv2ray with LIBV2RAY_PPROF=1"}`
}

// StopPprof в релизе ничего не делает.
func StopPprof() string {
	return `{"ok":true,"status":"not_compiled"}`
}

// PprofEnabled возвращает false в релизе.
func PprofEnabled() bool {
	return false
}
