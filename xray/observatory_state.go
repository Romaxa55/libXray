package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/features/extension"
)

// 2026-05-21: balancer winner logic вынесена в Dart-сторону.
//
// Изначально планировали возвращать current winner для каждого balancer'а из
// xray-config (BOOTSTRAP-BAL, auto-balancer). Через routing.BalancerSelector
// interface — но в pinned xray-core@v1.260327.1 этот интерфейс не существует
// (есть только BalancerOverrider/BalancerPrincipleTarget). PickOutbound() есть
// в `app/router/Balancer` но это конкретный тип, не interface, и доступ к нему
// через features/routing наружу не предусмотрен.
//
// Решение: Go отдаёт сырые `delay_ms` для всех узлов, Dart сам считает winner
// = min(delay_ms) среди alive=true. Это ровно та же логика что использует
// xray-core leastPing strategy внутри (strategy_leastping.go:30).
//
// Архитектурный плюс: native просто "telemetry source", вся логика на Dart →
// проще тестировать, проще менять без пересборки libXray.

// NodeJSON — состояние одного outbound'а из observatory'а. Поля совпадают
// с `observatory.OutboundStatus`, плюс развёрнутые HealthPing stats для
// плавной анимации (Average vs Max диапазон → толщина линии на карте).
type NodeJSON struct {
	Tag       string `json:"tag"`
	Alive     bool   `json:"alive"`
	DelayMs   int64  `json:"delay_ms"`
	LastErr   string `json:"last_error,omitempty"`
	LastSeen  int64  `json:"last_seen_ms,omitempty"`
	LastTry   int64  `json:"last_try_ms,omitempty"`
	PingAll   int64  `json:"ping_all,omitempty"`   // сколько раз пробили всего
	PingFail  int64  `json:"ping_fail,omitempty"`  // сколько фейлов из них
	PingAvg   int64  `json:"ping_avg,omitempty"`   // среднее RTT
	PingMax   int64  `json:"ping_max,omitempty"`   // макс RTT
	PingMin   int64  `json:"ping_min,omitempty"`   // мин RTT
	PingDev   int64  `json:"ping_deviation,omitempty"` // дисперсия (стабильность)
}

// ObservatoryStateResponse — итоговый JSON для Dart.
// nodes — состояние всех outbound'ов которые observatory мониторит
//   (т.е. перечислены в burstObservatory.subjectSelector).
// timestamp_ms — unix-ms когда снимок был взят (на стороне Go).
// error — заполнено если что-то пошло не так. nodes тогда пустой.
//
// Winner balancer'а Dart считает сам: min(delay_ms) среди alive=true.
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

	// 5. Конвертим []OutboundStatus → []NodeJSON.
	//    HealthPing nested message — может быть nil если observatory ещё
	//    не успел сделать ни одного round'а (cold start <30s после connect).
	nodes := make([]NodeJSON, 0, len(result.GetStatus()))
	for _, s := range result.GetStatus() {
		n := NodeJSON{
			Tag:      s.GetOutboundTag(),
			Alive:    s.GetAlive(),
			DelayMs:  s.GetDelay(),
			LastErr:  s.GetLastErrorReason(),
			LastSeen: s.GetLastSeenTime(),
			LastTry:  s.GetLastTryTime(),
		}
		if hp := s.GetHealthPing(); hp != nil {
			n.PingAll = hp.GetAll()
			n.PingFail = hp.GetFail()
			// HealthPing хранит durations в **наносекундах** (см.
			// xray-core/app/observatory/burst/healthping.go::getStatistics —
			// возвращает time.Duration). Мы делим на 1e6 чтобы Dart получил
			// миллисекунды, как и delay_ms сверху.
			n.PingAvg = hp.GetAverage() / 1_000_000
			n.PingMax = hp.GetMax() / 1_000_000
			n.PingMin = hp.GetMin() / 1_000_000
			n.PingDev = hp.GetDeviation() / 1_000_000
		}
		nodes = append(nodes, n)
	}
	resp.Nodes = nodes

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
