// gomobile_dep.go — blank import to keep golang.org/x/mobile in go.mod.
//
// gomobile bind для subpackage (не корневого) требует чтобы binding-пакеты
// были resolvable через main module's go.mod. Этот blank import форсит
// сохранение зависимости после go mod tidy.
//
// Без этого: `gomobile bind ./libv2ray` падает с
//   `unable to import bind: no Go package in golang.org/x/mobile/bind`

package libv2ray

import (
	_ "golang.org/x/mobile/bind"
)
