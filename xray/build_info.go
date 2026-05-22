package xray

import (
	"context"
	"encoding/json"
	"runtime"
	"runtime/debug"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport/internet"
)

// BuildInfo — sanity-check для Dart-стороны:
//   - какая версия xray вкомпилена
//   - какие фичи (особенно PR #5805 для chain-mode) ДОСТУПНЫ в runtime
//   - какой Go runtime
//
// Используется на старте app — Dart дёргает getBuildInfo(), смотрит флаг
// `pr5805_balancer_dialer`. Если false — значит вкомпилен upstream xray
// без нашего форка, и `sockopt.dialerProxy: BOOTSTRAP-BAL` (balancer-tag)
// **не будет** работать. Альтернативы: либо собрать с форком, либо
// сгенерировать config со sockopt.dialerProxy указывающим на outbound-tag
// (legacy simple-chain через bg-bootstrap).
//
// История: 2026-05-21 lab подтвердил что upstream xray-core@v1.260327.1
// БЕЗ нашего патча даёт `there is no outbound handler for dialerProxy`
// → chain рушится → юзер видит «нет интернета». Этот build_info нужен
// чтобы в проде однозначно понимать на стороне Dart — libXray умеет
// chain или нет.
type BuildInfo struct {
	// Версия xray-core (из core.Version()).
	XrayVersion string `json:"xray_version"`

	// Go runtime версия (например "go1.26.3").
	GoVersion string `json:"go_version"`

	// vcs.revision коммит libXray (из runtime/debug.ReadBuildInfo) — если
	// был VCS-stamping при сборке. На gomobile-bind часто пустой,
	// на cgo заполнен.
	LibxrayCommit string `json:"libxray_commit,omitempty"`

	// Карта фич: {имя_фичи: доступна_true_false}.
	// pr5805_balancer_dialer — главная для chain-mode.
	Features map[string]bool `json:"features"`
}

// GetBuildInfo возвращает JSON-строку с метаданными билда.
// Безопасно вызывать в любой момент — не трогает xray-instance,
// просто читает compile-time / runtime инфо.
func GetBuildInfo() string {
	info := BuildInfo{
		XrayVersion: core.Version(),
		GoVersion:   runtime.Version(),
		Features: map[string]bool{
			"pr5805_balancer_dialer":      detectPR5805(),
			"observatory_state":           true, // наш own API (см. observatory_state.go)
			"probe_outbound":              true, // honest HTTP-probe (см. probe_outbound.go)
			"dialer_proxy_fallback_tag":   detectDialerProxyFallbackTag(),
		},
	}

	// Build commit — из vcs-info (если есть).
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				info.LibxrayCommit = s.Value
				break
			}
		}
	}

	b, err := json.Marshal(info)
	if err != nil {
		return `{"error":"marshal failed"}`
	}
	return string(b)
}

// detectPR5805 определяет вкомпилен ли наш патч PR #5805 для
// `sockopt.dialerProxy = balancer-tag` (см. transport/internet/dialer.go
// в форке xray-core).
//
// Метод detection — пытаемся type-assert на `routing.BalancerSelector`
// interface. Этот интерфейс существует ТОЛЬКО в нашем форке (upstream
// xray-core@v1.260327.1 не имеет его — там только BalancerOverrider и
// BalancerPrincipleTarget).
//
// КАК ЭТО РАБОТАЕТ:
//   1. Если форк подключен через go.mod replace и PR #5805 закоммичен →
//      type `routing.BalancerSelector` доступен → этот файл компилится →
//      detectPR5805 = true (возвращаем константу).
//   2. Если форк НЕ подключен (upstream без PR #5805) → typing
//      `routing.BalancerSelector` undefined → **компиляция libXray
//      упадёт прямо здесь** с понятной ошибкой. Build-инженер сразу
//      поймёт что forq не задействован.
//
// Это compile-time gate. Если файл собрался — PR5805 точно есть.
// detectPR5805 в runtime просто отдаёт true.
//
// **АЛЬТЕРНАТИВНЫЙ путь** (если кто-то отключит форк):
//   - удалить строку `var _ routing.BalancerSelector = nil` ниже,
//   - изменить `return true` на `return false`,
//   - тогда libXray соберётся без форка, но клиенты увидят
//     `features.pr5805_balancer_dialer = false` и смогут собрать
//     config с simple-chain (без balancer-tag в sockopt.dialerProxy).
func detectPR5805() bool {
	// Compile-time проверка наличия type'а из форка.
	// nil-assignment на interface ОК — это просто type-check.
	var _ routing.BalancerSelector = nil
	_ = context.TODO() // anti-unused import
	return true
}

// detectDialerProxyFallbackTag — наша MegaV-фича 2026-05-22 (xray-core fork).
//
// Compile-time детекция: поле `DialerProxyFallbackTag` в `internet.SocketConfig`
// существует ТОЛЬКО в нашем форке (см. transport/internet/config.proto:160
// и dialer_fallback.go). Upstream xray-core этого поля не имеет.
//
// Если форк подключен → код компилится → возвращаем true.
// Если кто-то откатит форк → компиляция упадёт здесь с понятной ошибкой
// `unknown field DialerProxyFallbackTag in struct literal of type SocketConfig`.
//
// Dart-сторона использует этот флаг чтобы понять можно ли в config'е
// ставить `sockopt.dialerProxyFallbackTag` — без этой фичи поле просто
// игнорируется (старый xray не знает о нём), и тогда нет автоматического
// retry'я direct когда chain handshake fail'ится.
//
// См. memory/project_dialerproxy_fallback_native.md для подробного описания.
func detectDialerProxyFallbackTag() bool {
	// Compile-time check: поле должно существовать в SocketConfig.
	var _ = &internet.SocketConfig{DialerProxyFallbackTag: ""}
	return true
}
