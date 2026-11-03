#!/usr/bin/env bash
# build-core.sh — builds one MegaV core artifact from this libXray checkout and
# the xray-core fork pinned in xray-core.ref. Used by .github/workflows/build-cores.yml
# and by hand; it never deploys or uploads anything.
#
#   scripts/build-core.sh <target> <out-dir>
#
# Targets (what each app loads today):
#   android          libv2ray.aar + sources jar  gomobile bind ./libv2ray (compat shim)
#   linux-amd64      libXray.so + .h             c-shared of the root package (CGo* API)
#   linux-arm64      libXray.so + .h             same, arm64
#   windows-amd64    libv2ray.dll + .h           c-shared ./libv2ray_cgo, mingw cross
#   macos-universal  libv2ray.a + .h             c-archive ./libv2ray_cgo, arm64+amd64 lipo
# iOS (Libv2ray.xcframework) is not built here.
#
# Environment:
#   XRAY_CORE_DIR   xray-core fork checkout (default: ../xray-core next to libXray)
#   CORE_VERSION    version part of file names (default: <libXray tag or sha7>-xray-<sha7>)
#   ALLOW_XRAY_MISMATCH=1  build even if XRAY_CORE_DIR is not at xray-core.ref
#   CC              C compiler override (linux-arm64 cross, windows mingw)
#   ANDROID_NDK_HOME  NDK 27.x for android (default: newest $ANDROID_SDK_ROOT/ndk/27.*;
#                     a set ANDROID_NDK_HOME of another major is ignored)
#   MACOSX_DEPLOYMENT_TARGET  minimum macOS of the archive (default 12.0, Go 1.26 floor)
# Each build also writes buildinfo-<target>.json (go, ndk) into <out-dir>.
set -euo pipefail

TARGET="${1:?usage: build-core.sh <target> <out-dir>}"
OUT_DIR="${2:?usage: build-core.sh <target> <out-dir>}"

LIBXRAY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
XRAY_CORE_DIR="$(cd "${XRAY_CORE_DIR:-$LIBXRAY_DIR/../xray-core}" && pwd)"
mkdir -p "$OUT_DIR"
OUT_DIR="$(cd "$OUT_DIR" && pwd)"

want_ref="$(tr -d '[:space:]' < "$LIBXRAY_DIR/xray-core.ref")"
have_ref="$(git -C "$XRAY_CORE_DIR" rev-parse HEAD)"
if [ "$have_ref" != "$want_ref" ]; then
  if [ "${ALLOW_XRAY_MISMATCH:-0}" != "1" ]; then
    echo "[build-core] xray-core is at $have_ref, xray-core.ref wants $want_ref" >&2
    exit 1
  fi
  echo "[build-core] WARNING: xray-core at $have_ref, not $want_ref (ALLOW_XRAY_MISMATCH=1)" >&2
fi

"$LIBXRAY_DIR/scripts/check-no-timebomb.sh" "$XRAY_CORE_DIR"

if [ -z "${CORE_VERSION:-}" ]; then
  # CalVer tag as in release names, not its Go mirror v1.YYMMDD.N.
  lib_ver="$(git -C "$LIBXRAY_DIR" describe --tags --exact-match --exclude 'v1.*' 2>/dev/null || git -C "$LIBXRAY_DIR" rev-parse --short=7 HEAD)"
  CORE_VERSION="${lib_ver}-xray-${have_ref:0:7}"
fi
[[ "$CORE_VERSION" =~ ^[A-Za-z0-9._+-]+$ ]] || { echo "[build-core] bad CORE_VERSION: $CORE_VERSION" >&2; exit 2; }
NAME_SUFFIX="${CORE_VERSION}-${TARGET}"

# Build in a throwaway copy of the module: the root package is renamed to main
# for c-shared and the replace points at the absolute fork path, so neither
# the checkout nor the ../xray-core layout is touched.
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK:?}"' EXIT
SRC="$WORK/libXray"
mkdir -p "$SRC"
tar -C "$LIBXRAY_DIR" --exclude=./.git --exclude=./dist -cf - . | tar -C "$SRC" -xf -
cd "$SRC"
go mod edit -replace="github.com/xtls/xray-core=$XRAY_CORE_DIR"

export CGO_ENABLED=1
# Versions for GetBuildInfo (xray/build_info.go): core_version, xray_core_commit, libxray_commit.
lib_commit="$(git -C "$LIBXRAY_DIR" rev-parse HEAD)"
VERSION_LDFLAGS="-X github.com/xtls/libxray/xray.coreVersion=${CORE_VERSION} -X github.com/xtls/libxray/xray.xrayCoreCommit=${have_ref} -X github.com/xtls/libxray/xray.libxrayCommit=${lib_commit}"
GO_LDFLAGS="-s -w ${VERSION_LDFLAGS}"
NDK_VERSION=""

prepare_root_main() {
  cp build/template/main.gotemplate main.go
  for f in ./*.go; do
    sed -i.bak -E 's/^package[[:space:]]+libXray[[:space:]]*$/package main/' "$f"
    rm -f "$f.bak"
  done
}

case "$TARGET" in
  android)
    # The pinned gomobile reads only ANDROID_HOME for the SDK.
    sdk="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}"
    [ -z "$sdk" ] || export ANDROID_HOME="${ANDROID_HOME:-$sdk}"
    # NDK 27.x, as for the hand-built AARs; runner images change their default.
    case "$(basename "${ANDROID_NDK_HOME:-none}")" in
      27.*) ;;
      *)
        [ -n "$sdk" ] || { echo "[build-core] set ANDROID_SDK_ROOT or ANDROID_NDK_HOME (27.x)" >&2; exit 1; }
        ndks=()
        for d in "$sdk"/ndk/27.*; do [ -d "$d" ] && ndks+=("$d"); done
        [ "${#ndks[@]}" -gt 0 ] || { echo "[build-core] no NDK 27.x under $sdk/ndk" >&2; exit 1; }
        ANDROID_NDK_HOME="$(printf '%s\n' "${ndks[@]}" | sort -V | tail -1)"
        ;;
    esac
    export ANDROID_NDK_HOME
    NDK_VERSION="$(basename "$ANDROID_NDK_HOME")"
    echo "[build-core] NDK: $ANDROID_NDK_HOME"
    # gomobile and gobind at the version pinned in go.mod, not @latest. No
    # `gomobile init`: it installs gobind@latest over the pinned one, and bind
    # only needs gobind in PATH.
    export GOBIN="$WORK/bin"
    go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
    PATH="$GOBIN:$PATH"
    (cd libv2ray && gomobile bind -target android -androidapi 21 -trimpath \
      -ldflags="${VERSION_LDFLAGS} -extldflags=-Wl,-z,max-page-size=16384" -o "$WORK/libv2ray.aar" .)
    cp "$WORK/libv2ray.aar" "$OUT_DIR/libv2ray-${NAME_SUFFIX}.aar"
    cp "$WORK/libv2ray-sources.jar" "$OUT_DIR/libv2ray-${NAME_SUFFIX}-sources.jar"
    ;;
  linux-amd64|linux-arm64)
    arch="${TARGET#linux-}"
    prepare_root_main
    GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "$GO_LDFLAGS" -buildmode=c-shared \
      -o "$WORK/libXray.so" .
    cp "$WORK/libXray.so" "$OUT_DIR/libXray-${NAME_SUFFIX}.so"
    cp "$WORK/libXray.h" "$OUT_DIR/libXray-${NAME_SUFFIX}.h"
    ;;
  windows-amd64)
    CC="${CC:-x86_64-w64-mingw32-gcc}" GOOS=windows GOARCH=amd64 \
      go build -trimpath -ldflags "$GO_LDFLAGS" -buildmode=c-shared -o "$WORK/libv2ray.dll" ./libv2ray_cgo
    cp "$WORK/libv2ray.dll" "$OUT_DIR/libv2ray-${NAME_SUFFIX}.dll"
    cp "$WORK/libv2ray.h" "$OUT_DIR/libv2ray-${NAME_SUFFIX}.h"
    ;;
  macos-universal)
    export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-12.0}"
    for arch in arm64 amd64; do
      GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags "$GO_LDFLAGS" -buildmode=c-archive \
        -o "$WORK/$arch/libv2ray.a" ./libv2ray_cgo
    done
    lipo -create "$WORK/arm64/libv2ray.a" "$WORK/amd64/libv2ray.a" -output "$WORK/libv2ray.a"
    cp "$WORK/libv2ray.a" "$OUT_DIR/libv2ray-${NAME_SUFFIX}.a"
    cp "$WORK/arm64/libv2ray.h" "$OUT_DIR/libv2ray-${NAME_SUFFIX}.h"
    ;;
  *)
    echo "[build-core] unknown target: $TARGET" >&2
    exit 2
    ;;
esac

{
  printf '{"target":"%s","go":"%s"' "$TARGET" "$(go env GOVERSION)"
  [ -z "$NDK_VERSION" ] || printf ',"ndk":"%s"' "$NDK_VERSION"
  [ "$TARGET" != "macos-universal" ] || printf ',"macos_min":"%s"' "$MACOSX_DEPLOYMENT_TARGET"
  printf '}\n'
} > "$OUT_DIR/buildinfo-$TARGET.json"

echo "[build-core] $TARGET done:"
ls -l "$OUT_DIR"/*-"${NAME_SUFFIX}"*
