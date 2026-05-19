#!/usr/bin/env bash
# update-xray.sh — одна команда чтобы обновить xray и пересобрать ВСЁ.
#
# Что делает:
#   1. Опционально: обновляет pin xray-core коммита (если передан аргумент)
#   2. go mod tidy
#   3. Собирает Libv2ray.xcframework (gomobile) — для iOS
#   4. Собирает LibXray.xcframework (cgo) — для macOS/maccatalyst
#   5. Собирает libv2ray.a (cgo c-archive) — для macOS подключения через
#      vendored_libraries в v2ray_flutter.podspec
#   6. Подкладывает ВСЁ в нужные места:
#        - vpn_native_client/ios/Frameworks/Libv2ray.xcframework
#        - vpn_native_client/ios/Frameworks/LibXray.xcframework
#        - v2ray_flutter/ios/Frameworks/{Libv2ray,LibXray}.xcframework
#        - v2ray_flutter/libs/ios/Libv2ray.xcframework
#        - v2ray_flutter/macos/Classes/libv2ray.{a,h}
#        - v2ray_flutter/libs/macos/libv2ray.{a,h}
#   7. Печатает короткое summary
#
# Использование:
#   ./update-xray.sh                    # обновить с текущим pin'ом
#   ./update-xray.sh <commit-hash>      # обновить + поменять pin xray-core
#
# Требования:
#   - Go 1.25+ в PATH
#   - gomobile в $HOME/go/bin
#   - Python 3.13+
#   - Xcode + xcrun + lipo
#
# 2026-05-19: создан чтобы не делать ручное cp -R после каждой сборки.
# Заменяет шаги «прокачка через xcode + ручное подкладывание» одной кнопкой.

set -e

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$REPO_ROOT"

# Цвета для вывода
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log_step() { printf "${GREEN}▶ %s${NC}\n" "$*"; }
log_warn() { printf "${YELLOW}⚠ %s${NC}\n" "$*"; }
log_err()  { printf "${RED}✗ %s${NC}\n" "$*"; }

# Шаг 0: проверка тулзов
log_step "Проверка тулзов"
for tool in go gomobile python3 lipo xcodebuild xcrun; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    # gomobile может лежать в $HOME/go/bin — добавим в PATH автоматически
    if [ "$tool" = "gomobile" ] && [ -x "$HOME/go/bin/gomobile" ]; then
      export PATH="$HOME/go/bin:$PATH"
      log_warn "gomobile найден в \$HOME/go/bin, добавлен в PATH"
      continue
    fi
    log_err "$tool не найден в PATH"
    exit 1
  fi
done

# Шаг 1: опциональная смена pin'а xray-core
if [ -n "${1:-}" ]; then
  NEW_PIN="$1"
  log_step "Обновляю pin xray-core на @$NEW_PIN"
  # Меняем в build/app/build.py — это единственный источник правды для pin'а
  sed -i.bak "s|github.com/xtls/xray-core@[a-zA-Z0-9]\+|github.com/xtls/xray-core@$NEW_PIN|g" build/app/build.py
  rm -f build/app/build.py.bak
  log_step "Pin обновлён в build/app/build.py"
fi

# Шаг 2: основная сборка через apple all (gomobile + cgo + deploy xcframework'ов)
log_step "Собираю Libv2ray.xcframework (gomobile) + LibXray.xcframework (cgo)"
log_step "+ подкладываю xcframework'и в проект"
PATH="$HOME/go/bin:$PATH" python3 build/main.py apple all

# Шаг 3: cgo c-archive для macOS (libv2ray.a)
# build/main.py apple all НЕ собирает .a — нужен отдельный шаг для
# macOS-сборки через v2ray_flutter.podspec → force_load libv2ray.a.
log_step "Собираю libv2ray.a (c-archive universal arm64+amd64) для macOS"

BUILD_DIR="$(mktemp -d)"
trap "rm -rf $BUILD_DIR" EXIT

log_step "  └ arm64..."
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  go build -buildmode=c-archive -ldflags="-s -w" \
  -o "$BUILD_DIR/arm64/libv2ray.a" \
  ./libv2ray_cgo

log_step "  └ amd64..."
CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
  go build -buildmode=c-archive -ldflags="-s -w" \
  -o "$BUILD_DIR/amd64/libv2ray.a" \
  ./libv2ray_cgo

log_step "  └ lipo universal..."
lipo -create \
  "$BUILD_DIR/arm64/libv2ray.a" \
  "$BUILD_DIR/amd64/libv2ray.a" \
  -output "$BUILD_DIR/libv2ray.a"

# Шаг 4: подкладывание libv2ray.a + libv2ray.h в v2ray_flutter
log_step "Подкладываю libv2ray.a (universal) в v2ray_flutter"
MEGAV_ROOT="$(cd "$REPO_ROOT/.." && pwd)"
for dest in \
  "$MEGAV_ROOT/v2ray_flutter/macos/Classes" \
  "$MEGAV_ROOT/v2ray_flutter/libs/macos"
do
  if [ ! -d "$dest" ]; then
    log_warn "Skip $dest — не существует"
    continue
  fi
  cp "$BUILD_DIR/libv2ray.a" "$dest/libv2ray.a"
  cp "$BUILD_DIR/arm64/libv2ray.h" "$dest/libv2ray.h"
  printf "  → %s/libv2ray.{a,h}\n" "$dest"
done

# Шаг 5: summary
log_step "Готово!"
echo
LIBV2RAY_SIZE=$(du -sh "$MEGAV_ROOT/v2ray_flutter/macos/Classes/libv2ray.a" 2>/dev/null | awk '{print $1}')
XCFW_LIB=$(du -sh "$MEGAV_ROOT/vpn_native_client/ios/Frameworks/Libv2ray.xcframework" 2>/dev/null | awk '{print $1}')
XCFW_LX=$(du -sh "$MEGAV_ROOT/vpn_native_client/ios/Frameworks/LibXray.xcframework" 2>/dev/null | awk '{print $1}')
echo "  libv2ray.a (macOS universal):     $LIBV2RAY_SIZE"
echo "  Libv2ray.xcframework (iOS+mac):   $XCFW_LIB"
echo "  LibXray.xcframework  (iOS+mac):   $XCFW_LX"
echo
log_step "Дальше: cd ../vpn_native_client && flutter clean && cd macos && pod install && cd .. && flutter run -d macos"
