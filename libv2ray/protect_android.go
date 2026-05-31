//go:build android

// protect_android.go — Android VpnService.protect() мост для xray-сокетов.
//
// 2026-06-01: на Android xray-исходящие сокеты (к exit/bootstrap серверам)
// ДОЛЖНЫ обходить tun-интерфейс, иначе routing loop → краш. На iOS NE делает
// это сам на уровне системы. На Android — через VpnService.protect(fd).
//
// Раньше обход делался через addDisallowedApplication(packageName) — но он
// исключал ВЁСЬ пакет (UI + xray) → приложение (карта/радио/новости) шло мимо
// VPN, не как iOS. Правильный путь: protect ТОЛЬКО xray-сокеты, а UI-трафик
// пускать через туннель.
//
// Этот файл экспортит gomobile-friendly RegisterVpnProtector(p VpnProtector):
// Kotlin реализует VpnProtector (Protect(fd) → VpnService.protect(fd)) и
// регистрирует ПЕРЕД StartV2RayWithConfig. Тогда xray-dialer/listener сокеты
// защищаются на уровне fd, app-exclude не нужен, приложение идёт через VPN.
//
// gomobile экспортит как Libv2rayRegisterVpnProtector / интерфейс
// Libv2rayVpnProtector.

package libv2ray

import (
	libxray "github.com/xtls/libxray"
)

// VpnProtector — gomobile-интерфейс, реализуется в Kotlin
// (VpnTunnelService.protect(fd)). Возвращает true если protect успешен.
type VpnProtector interface {
	Protect(fd int) bool
}

// dialerControllerAdapter адаптирует VpnProtector под libxray.DialerController
// (ProtectFd(int) bool).
type dialerControllerAdapter struct {
	p VpnProtector
}

func (a *dialerControllerAdapter) ProtectFd(fd int) bool {
	return a.p.Protect(fd)
}

// RegisterVpnProtector регистрирует VpnService.protect-мост для xray-сокетов.
// Вызывать из Kotlin ПЕРЕД StartV2RayWithConfig. Защищает и dialer (исходящие
// к серверам), и listener сокеты. Экспорт: Libv2rayRegisterVpnProtector(p).
func RegisterVpnProtector(p VpnProtector) {
	if p == nil {
		return
	}
	adapter := &dialerControllerAdapter{p: p}
	libxray.RegisterDialerController(adapter)
	libxray.RegisterListenerController(adapter)
}
