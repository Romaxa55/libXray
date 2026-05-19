package xray

import (
	"strings"
	"sync"

	"github.com/xtls/xray-core/common/log"
)

// initLogFilterOnce гарантирует что глобальный log.Handler ставится один
// раз за жизнь процесса. RunXrayFromJSON может зваться много раз
// (reconnect, switch-server), но регистрировать handler нужно один.
var initLogFilterOnce sync.Once

// installSilentDeprecationFilter подменяет xray-core глобальный log.Handler
// на фильтрующий wrapper. Drop'ает шумные `[Warning] common/errors:
// The feature ... is deprecated, not recommended for using ...` сообщения
// которые xray-core печатает на КАЖДЫЙ ws/grpc-outbound (десятки строк
// мусора в логе на каждый connect).
//
// Backend всё ещё держит много ws/grpc серверов в pool'е, миграция на
// xhttp h2/h3 — отдельная задача (нужны новые серверы). До тех пор
// глушим warnings, они не несут полезной информации (серверы работают).
//
// Остальные xray-логи (Info/Warning/Error от proxy/dns/observatory)
// пропускаются как есть — мы фильтруем ТОЛЬКО deprecation-spam.
//
// Зовём из RunXrayFromJSON / RunXray (см. xray.go). Через sync.Once —
// идемпотентно, повторные вызовы no-op.
func installSilentDeprecationFilter() {
	initLogFilterOnce.Do(func() {
		log.RegisterHandler(&deprecationFilterHandler{})
	})
}

// deprecationFilterHandler — log.Handler который дропает строки про
// deprecated features и забивает остальное в stdout (как делал бы
// default xray log handler).
type deprecationFilterHandler struct{}

func (h *deprecationFilterHandler) Handle(msg log.Message) {
	s := msg.String()
	// Сообщения от feature_errors.go: PrintNonRemovalDeprecatedFeatureWarning
	// и PrintDeprecatedFeatureWarning. Оба содержат "is deprecated".
	if strings.Contains(s, "is deprecated, not recommended for using") {
		return
	}
	if strings.Contains(s, "is deprecated, will be removed soon") {
		return
	}
	// Остальное — печатаем как обычно (xray-core default — stdout).
	println(s)
}
