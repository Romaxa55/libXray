package errors

// MegaV-patch 2026-05-19: deprecated-feature warnings выпилены.
// Backend всё ещё держит много ws/grpc серверов в pool'е, миграция на
// xhttp h2/h3 — отдельная задача (нужны новые серверы). До тех пор
// эти warnings — только шум в логе (десятки строк на каждый connect),
// они не несут полезной информации (серверы работают).
//
// Функции оставлены как no-op чтобы не ломать call-sites в xray-core
// (transport_internet.go, trojan.go, vmess.go, shadowsocks.go и т.д.).

// PrintNonRemovalDeprecatedFeatureWarning — no-op в MegaV.
// Do not remove this function even there is no reference to it.
func PrintNonRemovalDeprecatedFeatureWarning(sourceFeature string, targetFeature string) {
	_ = sourceFeature
	_ = targetFeature
}

// PrintDeprecatedFeatureWarning — no-op в MegaV.
// Do not remove this function even there is no reference to it.
func PrintDeprecatedFeatureWarning(feature string, migrateFeature string) {
	_ = feature
	_ = migrateFeature
}

// PrintRemovedFeatureError — оставлено как было.
// Этот возвращает реальную ошибку, не warning, и используется когда
// фича РЕАЛЬНО удалена. Мы её не глушим — пусть xray корректно
// сообщит что фича недоступна, иначе пользователь не поймёт почему
// сервер не работает.
func PrintRemovedFeatureError(feature string, migrateFeature string) error {
	if len(migrateFeature) > 0 {
		return New("The feature " + feature + " has been removed and migrated to " + migrateFeature + ". Please update your config(s) according to release note and documentation.")
	}
	return New("The feature " + feature + " has been removed. Please update your config(s) according to release note and documentation.")
}
