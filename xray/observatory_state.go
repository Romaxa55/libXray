package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/features/extension"
)

// 2026-05-22: контракт NE → Dart упрощён до минимально-необходимого.
//
// Dart получает 4 поля на узел (tag, role, alive, delay_ms) и сам делает
// геолокацию через `data_v5.dat` (sync SQL lookup по id из тега).
// Winner определяется НА СТОРОНЕ GO (через role=active_*) — Dart не считает
// minDelay. Это упрощает Dart-логику и убирает дублирование (раньше Dart
// делал тот же minDelay alive что и xray observatory leastPing strategy).
//
// Никаких живых HTTP-probe на ip.megav.app — координаты/страны уже в БД.

// NodeJSON — минимальный контракт на узел.
//
//	tag      — outbound tag из xray-config ("bs-391", "server-7716", и т.д.)
//	role     — "bootstrap" | "active_bootstrap" | "exit" | "active_exit" | "other"
//	           active_* = winner в своей категории (min delay_ms среди alive).
//	alive    — *bool (true/false/null). null = observatory ещё не пробовал
//	           этот outbound (cold start, ping_all == 0). Dart рисует unknown.
//	delay_ms — RTT в миллисекундах для tooltip / RTT-надписи на маршруте.
//	           0 если alive=false или null.
//	route    — реальный маршрут последнего dial'а этого outbound'а.
//	           "" = ещё не dial'или (или это служебный outbound: direct/block/dns-out)
//	           "via:bs-N"     = chain работает, dial через указанный bs
//	           "fallback:direct" = chain свалился, dial идёт через direct (наш
//	                              dialerProxyFallback feature сработал)
//	           "direct"       = config БЕЗ dialerProxy (legacy mode), прямой dial
//	           Dart использует это для отрисовки реальных линий на карте:
//	           "via:bs-N" → линия exit→bs→user
//	           "fallback:..." → пунктир exit→user (без bs, отражает обход chain)
type NodeJSON struct {
	Tag     string `json:"tag"`
	Role    string `json:"role"`
	Alive   *bool  `json:"alive"` // pointer → JSON-encoder выдаст null для unknown
	DelayMs int64  `json:"delay_ms"`
	Route   string `json:"route,omitempty"` // "" = нет данных, omitempty для compact JSON
}

// Role строки. Константы вместо magic strings — чтобы менять централизованно
// и не разбегаться по проекту с typos типа "active-bootstrap" vs "active_bootstrap".
const (
	RoleBootstrap       = "bootstrap"
	RoleActiveBootstrap = "active_bootstrap"
	RoleExit            = "exit"
	RoleActiveExit      = "active_exit"
	RoleOther           = "other" // direct, block, dns-out, fragment-*, и т.п.
)

// ObservatoryStateResponse — итоговый JSON для Dart.
// nodes — все outbound'ы из subjectSelector с ролями и alive-статусом.
// timestamp_ms — unix-ms (когда снимок взят, Go-side).
// error — заполнено если что-то пошло не так. nodes тогда пустой.
type ObservatoryStateResponse struct {
	Nodes       []NodeJSON `json:"nodes"`
	TimestampMs int64      `json:"timestamp_ms"`
	Error       string     `json:"error,omitempty"`
}

// GetObservatoryState — snapshot текущего состояния observatory + active
// winners у указанных balancer'ов. Возвращает JSON-строку.
//
// Зачем нужно: для визуализации chain'а на карте в UI. Каждый узел
// (bs-N, server-N) показывается с цветом по alive/dead, активный winner
// подсвечен ярко. RTT обновляется → толщина/anim-скорость линии меняется.
//
// КРИТИЧНО: ходит ИСКЛЮЧИТЕЛЬНО внутрь xray-instance, никаких внешних
// HTTP-запросов. Observatory уже делает свои probe'ы в фоне (с интервалом
// VpnLimits.observatoryInterval = 30s через https://www.google.com/generate_204).
// Поэтому periodic вызовы этого метода НЕ создают сетевой трафик и НЕ
// раздражают exit-сервера (которые могли бы забанить за частые probe).
//
// Параметры:
//
//	requestJSON — base64-кодированный или сырой JSON (ObservatoryStateRequest).
//	              Поддерживаем оба формата для cgo-binding'ов где удобнее
//	              base64 (Android JNI) и для gomobile где OK raw JSON.
//	              См. handleRequestJSON.
//
// Возвращаемый JSON:
//
//	{
//	  "nodes": [
//	    {"tag":"bs-0","alive":true,"delay_ms":145,"ping_all":12,"ping_fail":0,
//	     "ping_avg":140,"ping_max":190,"ping_min":110,"ping_deviation":20},
//	    {"tag":"bs-1","alive":false,"delay_ms":0,"last_error":"i/o timeout", ...},
//	    {"tag":"server-0","alive":true,"delay_ms":234, ...}
//	  ],
//	  "timestamp_ms": 1779324408901
//	}
//
// Winner = min(delay_ms) среди alive=true. Считает Dart.
//
// Ошибки (всегда в формате response JSON, не throw):
//   - "xray not running" — coreServer == nil или !IsRunning()
//   - "observatory not configured" — burstObservatory нет в config'е
//   - "GetObservation failed: ..." — feature вернул ошибку
//   - "invalid request JSON" — невалидный input
func GetObservatoryState(requestJSON string) string {
	resp := ObservatoryStateResponse{
		TimestampMs: time.Now().UnixMilli(),
	}

	// 1. Reserve: пока request пустой. Если пришёл невалидный JSON — fail-fast
	//    чтобы будущие caller'ы заметили опечатку в request schema.
	if requestJSON != "" {
		var anyJSON map[string]interface{}
		if err := json.Unmarshal([]byte(requestJSON), &anyJSON); err != nil {
			resp.Error = fmt.Sprintf("invalid request JSON: %v", err)
			return marshalObservatoryResp(resp)
		}
		_ = anyJSON
	}

	// 2. Snapshot xray instance — atomic как в ProbeOutbound.
	//    Защита от concurrent StopXray.
	syncOnce.Do(syncCoreServerPtr)
	inst := loadCoreServer()
	if inst == nil || !inst.IsRunning() {
		resp.Error = "xray not running"
		return marshalObservatoryResp(resp)
	}

	// 3. Берём observatory feature. ObservatoryType() возвращает
	//    (*extension.Observatory)(nil) — это interface-type assertion key.
	//    GetFeature возвращает features.Feature (== Observatory) или nil.
	obsFeature := inst.GetFeature(extension.ObservatoryType())
	if obsFeature == nil {
		resp.Error = "observatory not configured (burstObservatory missing in xray config)"
		return marshalObservatoryResp(resp)
	}
	obs, ok := obsFeature.(extension.Observatory)
	if !ok {
		// Не должно случиться: ObservatoryType() гарантирует тип.
		// Но защита от будущих изменений xray-core API — лучше fail soft.
		resp.Error = "observatory feature wrong type"
		return marshalObservatoryResp(resp)
	}

	// 4. Snapshot observation. Возвращает proto.Message — это
	//    *observatory.ObservationResult (см. burst/burstobserver.go:32).
	msg, err := obs.GetObservation(context.Background())
	if err != nil {
		resp.Error = fmt.Sprintf("GetObservation failed: %v", err)
		return marshalObservatoryResp(resp)
	}
	result, ok := msg.(*observatory.ObservationResult)
	if !ok {
		resp.Error = "unexpected observation type"
		return marshalObservatoryResp(resp)
	}

	// 5. Конвертим []OutboundStatus → []NodeJSON в два прохода:
	//
	//    Pass 1: собираем сырые данные (tag, alive*, delay). alive — pointer,
	//    null когда observatory ещё не пробовала этот outbound (ping_all == 0
	//    из HealthPing → cold start, статус unknown). False → точно мёртв.
	//
	//    Pass 2: находим winner'ов (bs-* и server-* отдельно) — min delay
	//    среди alive=true. Это та же логика что и leastPing strategy в xray
	//    (strategy_leastping.go:30), но применённая ЗДЕСЬ чтобы пометить role
	//    как active_*. Dart не должен дублировать вычисление winner'а.
	statuses := result.GetStatus()
	rawNodes := make([]NodeJSON, 0, len(statuses))

	for _, s := range statuses {
		tag := s.GetOutboundTag()
		delay := s.GetDelay()

		// alive: null если observatory не делала probe (ping_all==0).
		// xray ставит status.Alive=false в нескольких случаях:
		//  (a) реальный fail — пинг был, не прошёл
		//  (b) cold start — observation добавлен в массив но еще ни одной попытки
		// Чтобы различать — смотрим healthping.GetAll(). Если 0 — это cold start,
		// alive=null. Иначе доверяем s.GetAlive().
		var aliveVal *bool
		if hp := s.GetHealthPing(); hp != nil && hp.GetAll() > 0 {
			v := s.GetAlive()
			aliveVal = &v
		} else if delay > 0 {
			// Edge case: HealthPing nil/empty но delay есть → значит пинг был
			// и прошёл (xray-core fills delay только при успешном пинге).
			// Считаем alive=true.
			t := true
			aliveVal = &t
		}
		// иначе aliveVal остаётся nil → JSON encode null → Dart рисует unknown

		rawNodes = append(rawNodes, NodeJSON{
			Tag:     tag,
			Alive:   aliveVal,
			DelayMs: delay,
			Route:   getRoute(tag), // "" если ни разу не dial'или
		})
	}

	// Pass 2: winner'ы (min delay среди alive=true) для двух категорий.
	bsWinnerIdx, exitWinnerIdx := -1, -1
	var bsWinnerDelay, exitWinnerDelay int64 = -1, -1
	for i, n := range rawNodes {
		if n.Alive == nil || !*n.Alive {
			continue
		}
		// delay_ms может быть 0 для свежеподнятого alive выше (edge case без
		// healthping). Это не «лучший» сервер — пропускаем как кандидата на
		// winner'а если у нас уже есть кандидат с положительным delay.
		if n.DelayMs <= 0 {
			continue
		}
		switch {
		case strings.HasPrefix(n.Tag, "bs-"):
			if bsWinnerIdx == -1 || n.DelayMs < bsWinnerDelay {
				bsWinnerIdx, bsWinnerDelay = i, n.DelayMs
			}
		case strings.HasPrefix(n.Tag, "server-"):
			if exitWinnerIdx == -1 || n.DelayMs < exitWinnerDelay {
				exitWinnerIdx, exitWinnerDelay = i, n.DelayMs
			}
		}
	}

	// Pass 3: проставляем role. Категория по prefix tag'а, active_* для winner'ов.
	for i := range rawNodes {
		tag := rawNodes[i].Tag
		switch {
		case strings.HasPrefix(tag, "bs-"):
			if i == bsWinnerIdx {
				rawNodes[i].Role = RoleActiveBootstrap
			} else {
				rawNodes[i].Role = RoleBootstrap
			}
		case strings.HasPrefix(tag, "server-"):
			if i == exitWinnerIdx {
				rawNodes[i].Role = RoleActiveExit
			} else {
				rawNodes[i].Role = RoleExit
			}
		default:
			rawNodes[i].Role = RoleOther
		}
	}

	resp.Nodes = rawNodes
	return marshalObservatoryResp(resp)
}

func marshalObservatoryResp(r ObservatoryStateResponse) string {
	b, err := json.Marshal(r)
	if err != nil {
		// Marshal-failure теоретически возможен только при NaN/Inf в float'ах,
		// которых у нас нет. Но если что — отдадим хоть error-структуру.
		return fmt.Sprintf(`{"nodes":[],"timestamp_ms":%d,"error":"marshal failed: %s"}`,
			r.TimestampMs, err.Error())
	}
	return string(b)
}
