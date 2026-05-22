// MegaV addition (2026-05-22): route tracking для observability.
//
// xray-core fork emit'ит route notifications через transport/internet.
// SetRouteNotifier. Этот файл регистрирует callback который **сохраняет
// последний** маршрут каждого outbound'а в lock-protected map, и отдаёт
// его в observatory_state JSON.
//
// Используется Dart'ом для отрисовки реальных маршрутов на карте:
//   - "via:bs-163" — chain работает, exit идёт через bs-163
//   - "fallback:direct" — chain свалился, exit dial'ится через direct
//   - "direct" — нет chain by config, exit прямой
//
// Last-write-wins семантика. Если у server-391 одновременно 30 коннектов
// (юзер открыл 30 вкладок) — все они идут через один outbound, последний
// перетирает state. Это нормально — мы показываем «как идёт **сейчас**»,
// а не статистику.

package xray

import (
	"sync"

	"github.com/xtls/xray-core/transport/internet"
)

type routingState struct {
	mu     sync.RWMutex
	routes map[string]string // outboundTag → route ("via:bs-N" | "fallback:..." | "direct")
}

var globalRoutingState = &routingState{
	routes: make(map[string]string),
}

// recordRoute — callback который xray дёргает на каждое изменение
// маршрута. Очень hot path (тысячи вызовов в минуту на активном VPN),
// поэтому только atomic-ish update map.
func recordRoute(outboundTag, route string) {
	globalRoutingState.mu.Lock()
	globalRoutingState.routes[outboundTag] = route
	globalRoutingState.mu.Unlock()
}

// getRoute — читаем текущий маршрут outbound'а. "" если ни разу не
// dial'или (= новый outbound, observatory ещё не запускала pinger'ов).
func getRoute(outboundTag string) string {
	globalRoutingState.mu.RLock()
	defer globalRoutingState.mu.RUnlock()
	return globalRoutingState.routes[outboundTag]
}

// resetRoutes — сбросить state. Вызывается при xray restart чтобы старые
// маршруты от прошлой сессии не светились в observatory snapshot.
func resetRoutes() {
	globalRoutingState.mu.Lock()
	// Создаём свежую map (а не clear), чтобы прошлая не держалась через
	// мелкие GC pointers — экономия памяти при долгих сессиях.
	globalRoutingState.routes = make(map[string]string)
	globalRoutingState.mu.Unlock()
}

// init регистрирует наш recordRoute как глобальный xray RouteNotifier.
// Вызывается один раз при первой загрузке Go runtime'а libXray.
func init() {
	internet.SetRouteNotifier(recordRoute)
}
