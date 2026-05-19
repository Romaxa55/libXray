#!/usr/bin/env bash
# update-xray.sh — одна команда чтобы обновить xray и пересобрать ВСЁ.
#
# Использование:
#   ./update-xray.sh                     # apple (default, на macOS дев-машине)
#   ./update-xray.sh apple               # iOS + macOS + tvOS + maccatalyst
#   ./update-xray.sh android             # Android AAR (требует NDK)
#   ./update-xray.sh linux               # libXray.so (только на Linux/Docker!)
#   ./update-xray.sh windows             # libXray.dll (только на Win/MinGW/Docker!)
#   ./update-xray.sh all                 # все доступные на текущей ОС
#
#   ./update-xray.sh apple <commit>      # обновить pin xray-core и пересобрать
#   ./update-xray.sh all <commit>        # то же для all
#
# Что делает (apple):
#   1. python3 build/main.py apple all   (gomobile + cgo + auto-deploy xcfw)
#   2. CGO c-archive libv2ray.a universal (arm64+amd64) → v2ray_flutter macos
#
# Что делает (android):
#   3. python3 build/main.py android all (libXray.aar + libv2ray.aar)
#   4. cp libv2ray.aar → v2ray_flutter/android/libs/
#
# Что делает (linux/windows):
#   5. python3 build/main.py linux|windows
#   6. cp .so/.dll → v2ray_flutter/{linux,windows}/libs/
#                  + vpn_native_client/windows/runner/libs/
#
# Требования:
#   apple:   Go 1.25+, gomobile, Python 3, Xcode + xcrun + lipo
#   android: + Android NDK ($ANDROID_NDK_HOME)
#   linux:   GCC (нативно или через Docker на macOS)
#   windows: MinGW-w64 (только через Docker на macOS)
#
# 2026-05-19: создан чтобы не делать ручное cp -R после каждой сборки.

set -e

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$REPO_ROOT"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

log_step()    { printf "${GREEN}▶ %s${NC}\n" "$*"; }
log_substep() { printf "  ${BLUE}└ %s${NC}\n" "$*"; }
log_warn()    { printf "${YELLOW}⚠ %s${NC}\n" "$*"; }
log_err()     { printf "${RED}✗ %s${NC}\n" "$*"; }

# Распарсим аргументы: первый — платформа, второй (опц.) — pin xray-core
PLATFORM="${1:-apple}"
NEW_PIN="${2:-}"
MEGAV_ROOT="$(cd "$REPO_ROOT/.." && pwd)"

# gomobile может лежать в $HOME/go/bin — добавим в PATH
if ! command -v gomobile >/dev/null 2>&1 && [ -x "$HOME/go/bin/gomobile" ]; then
  export PATH="$HOME/go/bin:$PATH"
fi

# === Опционально: смена pin'а xray-core ===
if [ -n "$NEW_PIN" ]; then
  log_step "Обновляю pin xray-core → @$NEW_PIN"
  sed -i.bak "s|github.com/xtls/xray-core@[a-zA-Z0-9]\+|github.com/xtls/xray-core@$NEW_PIN|g" build/app/build.py
  rm -f build/app/build.py.bak
fi

# =====================================================================
# Apply local patches over xray-core
# =====================================================================
# Что патчим:
#   common/errors/feature_errors.go — превращает PrintNonRemovalDeprecated*
#   и PrintDeprecated* в no-op. Иначе xray валит десятки строк
#   `WebSocket transport is deprecated` в stdout на каждый connect (на
#   каждый ws/grpc outbound), что мы не можем глушить через log.Handler
#   потому что app/log при старте xray затирает наш handler своим.
#
# Vendor создаётся перед `go build`, патч копируется поверх. После
# сборки vendor удаляется (40 МБ, не нужен в git). Это сохраняет patches/
# в репо чистыми и не пихает 40 МБ копии xray-core.
apply_xray_patches() {
  if [ ! -d patches/xray-core-overlay ]; then
    return 0  # нет патчей — пропустить
  fi
  log_step "Применяю патчи xray-core (patches/xray-core-overlay/)"
  # Если vendor уже есть (после ошибки прошлого билда) — обновим, не пересоздаём
  if [ ! -d vendor ]; then
    log_substep "go mod vendor..."
    go mod vendor >/dev/null 2>&1
  fi
  # Копируем каждый файл patches/xray-core-overlay/* → vendor/github.com/xtls/xray-core/*
  find patches/xray-core-overlay -type f | while read -r src; do
    rel="${src#patches/xray-core-overlay/}"
    dst="vendor/github.com/xtls/xray-core/$rel"
    if [ -f "$dst" ]; then
      cp "$src" "$dst"
      log_substep "patched: $rel"
    else
      log_warn "skip patch (target not found): $rel"
    fi
  done
}

cleanup_vendor() {
  if [ -d vendor ]; then
    rm -rf vendor
  fi
}

# Применяем патчи в начале каждой сборки (для Apple/Android/Linux/Windows)
trap cleanup_vendor EXIT

# =====================================================================
# Apple
# =====================================================================
build_apple() {
  apply_xray_patches
  log_step "Apple: Libv2ray.xcframework + LibXray.xcframework + libv2ray.a"
  PATH="$HOME/go/bin:$PATH" python3 build/main.py apple all

  log_step "libv2ray.a universal (arm64+amd64) для macOS"
  local BUILD_DIR
  BUILD_DIR="$(mktemp -d)"
  log_substep "arm64..."
  CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
    go build -buildmode=c-archive -ldflags="-s -w" \
    -o "$BUILD_DIR/arm64/libv2ray.a" ./libv2ray_cgo

  log_substep "amd64..."
  CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
    go build -buildmode=c-archive -ldflags="-s -w" \
    -o "$BUILD_DIR/amd64/libv2ray.a" ./libv2ray_cgo

  log_substep "lipo universal..."
  lipo -create "$BUILD_DIR/arm64/libv2ray.a" "$BUILD_DIR/amd64/libv2ray.a" \
    -output "$BUILD_DIR/libv2ray.a"

  for dest in \
    "$MEGAV_ROOT/v2ray_flutter/macos/Classes" \
    "$MEGAV_ROOT/v2ray_flutter/libs/macos"
  do
    [ -d "$dest" ] || { log_warn "Skip $dest"; continue; }
    cp "$BUILD_DIR/libv2ray.a" "$dest/libv2ray.a"
    cp "$BUILD_DIR/arm64/libv2ray.h" "$dest/libv2ray.h"
    log_substep "→ $dest/libv2ray.{a,h}"
  done

  rm -rf "$BUILD_DIR"
}

# =====================================================================
# Android
# =====================================================================
build_android() {
  apply_xray_patches
  log_step "Android: libXray.aar + libv2ray.aar (compat shim)"
  if [ -z "${ANDROID_NDK_HOME:-}" ] && [ ! -d "$HOME/Library/Android/sdk/ndk" ]; then
    log_warn "ANDROID_NDK_HOME не задан и NDK не найден — gomobile может упасть"
  fi
  PATH="$HOME/go/bin:$PATH" python3 build/main.py android all

  log_step "Подкладываю AAR'ы в v2ray_flutter"
  local DEST="$MEGAV_ROOT/v2ray_flutter/android/libs"
  if [ -d "$DEST" ]; then
    if [ -f "$REPO_ROOT/libv2ray.aar" ]; then
      cp "$REPO_ROOT/libv2ray.aar" "$DEST/libv2ray.aar"
      log_substep "→ $DEST/libv2ray.aar"
    fi
    if [ -f "$REPO_ROOT/libXray.aar" ]; then
      cp "$REPO_ROOT/libXray.aar" "$DEST/libXray.aar"
      log_substep "→ $DEST/libXray.aar (если v2ray_flutter его подхватывает)"
    fi
  else
    log_warn "$DEST не существует"
  fi
}

# =====================================================================
# Linux
# =====================================================================
build_linux() {
  apply_xray_patches
  log_step "Linux: libXray.so (x86_64 + arm64)"
  if [ "$(uname)" != "Linux" ]; then
    log_warn "Текущая ОС — $(uname). libXray Linux-сборка ХОЧЕТ нативный gcc."
    log_warn "На macOS это сломается. Запускай этот шаг в Docker:"
    log_warn "  docker run --rm -v \$(pwd)/..:/work -w /work/libXray golang:1.25-bookworm \\"
    log_warn "    bash -c 'apt-get update && apt-get install -y gcc && python3 build/main.py linux'"
    return 1
  fi
  PATH="$HOME/go/bin:$PATH" python3 build/main.py linux

  log_step "Подкладываю .so в v2ray_flutter"
  local DEST="$MEGAV_ROOT/v2ray_flutter/linux/libs"
  if [ -d "$DEST" ]; then
    [ -f "$REPO_ROOT/linux_so/libXray.so" ] && \
      cp "$REPO_ROOT/linux_so/libXray.so" "$DEST/libXray.so" && \
      log_substep "→ $DEST/libXray.so"
    [ -f "$REPO_ROOT/linux_so/libXray-arm64.so" ] && \
      cp "$REPO_ROOT/linux_so/libXray-arm64.so" "$DEST/libXray-arm64.so" && \
      log_substep "→ $DEST/libXray-arm64.so"
  fi
}

# =====================================================================
# Windows
# =====================================================================
build_windows() {
  apply_xray_patches
  log_step "Windows: libXray.dll"
  if [ "$(uname)" != "Windows_NT" ] && ! command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
    log_warn "Не Windows и нет MinGW-w64 (x86_64-w64-mingw32-gcc)."
    log_warn "Для cross-build на macOS поставь: brew install mingw-w64"
    log_warn "Или используй Docker: docker run --rm -v \$(pwd)/..:/work -w /work/libXray \\"
    log_warn "  golang:1.25-bookworm bash -c 'apt-get install -y gcc-mingw-w64 && python3 build/main.py windows'"
    return 1
  fi
  PATH="$HOME/go/bin:$PATH" python3 build/main.py windows

  log_step "Подкладываю libXray.dll в проект"
  for dest in \
    "$MEGAV_ROOT/v2ray_flutter/windows/libs" \
    "$MEGAV_ROOT/vpn_native_client/windows/runner/libs"
  do
    [ -d "$dest" ] || { log_warn "Skip $dest"; continue; }
    if [ -f "$REPO_ROOT/windows_dll/libXray.dll" ]; then
      cp "$REPO_ROOT/windows_dll/libXray.dll" "$dest/libXray.dll"
      log_substep "→ $dest/libXray.dll"
    fi
  done
}

# =====================================================================
# Router (entry-point)
# =====================================================================
case "$PLATFORM" in
  apple)
    build_apple
    ;;
  android)
    build_android
    ;;
  linux)
    build_linux
    ;;
  windows)
    build_windows
    ;;
  all)
    log_step "Сборка ВСЕХ платформ доступных на $(uname)"
    build_apple || log_err "apple failed"
    build_android || log_err "android failed (нужен NDK?)"
    build_linux 2>/dev/null || log_warn "linux skipped (не Linux ОС)"
    build_windows 2>/dev/null || log_warn "windows skipped (нет MinGW)"
    ;;
  -h|--help|help)
    grep "^#" "$0" | head -30
    exit 0
    ;;
  *)
    log_err "Неизвестная платформа: $PLATFORM"
    log_err "Используй: apple | android | linux | windows | all"
    exit 1
    ;;
esac

echo
log_step "Готово!"
echo
LIBV2RAY_A=$(du -sh "$MEGAV_ROOT/v2ray_flutter/macos/Classes/libv2ray.a" 2>/dev/null | awk '{print $1}')
XCFW=$(du -sh "$MEGAV_ROOT/vpn_native_client/ios/Frameworks/Libv2ray.xcframework" 2>/dev/null | awk '{print $1}')
AAR=$(du -sh "$MEGAV_ROOT/v2ray_flutter/android/libs/libv2ray.aar" 2>/dev/null | awk '{print $1}')
SO=$(du -sh "$MEGAV_ROOT/v2ray_flutter/linux/libs/libXray.so" 2>/dev/null | awk '{print $1}')
DLL=$(du -sh "$MEGAV_ROOT/v2ray_flutter/windows/libs/libXray.dll" 2>/dev/null | awk '{print $1}')

[ -n "$LIBV2RAY_A" ] && echo "  macOS libv2ray.a:               $LIBV2RAY_A"
[ -n "$XCFW" ]       && echo "  iOS/mac Libv2ray.xcframework:   $XCFW"
[ -n "$AAR" ]        && echo "  Android libv2ray.aar:           $AAR"
[ -n "$SO" ]         && echo "  Linux libXray.so:               $SO"
[ -n "$DLL" ]        && echo "  Windows libXray.dll:            $DLL"

echo
log_step "Дальше:"
echo "  cd ../vpn_native_client"
echo "  flutter clean && cd macos && pod install && cd .. && flutter run -d macos"
