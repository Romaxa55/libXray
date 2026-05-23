"""
Apple-сборщик: собирает оба нужных артефакта для iOS + macOS в одном проходе.

★ ВЫХОДНЫЕ АРТЕФАКТЫ (в libXray/):
  1. Libv2ray.xcframework — gomobile bind libv2ray/ subpackage
     (iOS device + iOS sim + maccatalyst + macOS-as-iOS framework)
  2. macos/libv2ray.a (universal arm64+amd64) + macos/libv2ray.h
     — cgo c-archive из libv2ray_cgo/ subpackage для macOS Flutter

Деплой производит app/apple_deploy.py в production пути:
  - v2ray_flutter/ios/Frameworks/Libv2ray.xcframework
  - v2ray_flutter/macos/Classes/libv2ray.{a,h}

2026-05-19 фикс:
  - gomobile bind теперь биндит ПОДПАКЕТ libv2ray/, не lib_dir (корень).
    В корне `package libXray` (или main для cgo-этапа) — gomobile bind
    его не поддерживает.
  - macOS libv2ray.a интегрирован в python-сборку (раньше был shell в
    update-xray.sh — теперь один python-скрипт делает всё).
"""

import os
import shutil
import subprocess

from app.build import Builder


class AppleGoMobileBuilder(Builder):
    """gomobile bind libv2ray/ + cgo build libv2ray.a (macOS)."""

    def before_build(self):
        super().before_build()  # init_go_env (чистый upstream xray-core) + download_geo
        # Чистим old build outputs.
        self.clean_lib_dirs(["Libv2ray.xcframework"])
        libv2ray_xcf = os.path.join(self.lib_dir, "libv2ray", "Libv2ray.xcframework")
        if os.path.isdir(libv2ray_xcf):
            print(f"[apple_gomobile] cleaning {libv2ray_xcf}")
            shutil.rmtree(libv2ray_xcf)
        # macOS libv2ray.a output dir
        macos_dir = os.path.join(self.lib_dir, "macos")
        if os.path.isdir(macos_dir):
            shutil.rmtree(macos_dir)
        os.makedirs(macos_dir, exist_ok=True)

        self.prepare_gomobile()

    def build(self):
        self.before_build()

        # =====================================================================
        # 1. gomobile bind для iOS xcframework
        # =====================================================================
        libv2ray_dir = os.path.join(self.lib_dir, "libv2ray")
        if not os.path.isdir(libv2ray_dir):
            raise Exception(f"libv2ray/ subpackage not found: {libv2ray_dir}")

        # 2026-05-23 (юзер): env-var LIBV2RAY_PPROF=1 вкомпиливает
        # net/http/pprof в Libv2ray.xcframework. Включает StartPprof() /
        # StopPprof() из libv2ray/pprof.go (build tag pprof_enabled).
        # Без env-var → stub из libv2ray/pprof_stub.go (релиз).
        # См. libv2ray/pprof.go header для инструкции iproxy.
        build_tags = []
        if os.environ.get("LIBV2RAY_PPROF") == "1":
            build_tags.append("pprof_enabled")
            print("[apple_gomobile] LIBV2RAY_PPROF=1 → adding -tags pprof_enabled")

        gomobile_cmd = [
            "gomobile", "bind",
            "-target", "ios,iossimulator,macos,maccatalyst",
            "-iosversion", "15.0",
        ]
        if build_tags:
            gomobile_cmd.extend(["-tags", ",".join(build_tags)])
        gomobile_cmd.append(".")

        print(f"[apple_gomobile] gomobile bind {libv2ray_dir}")
        print(f"[apple_gomobile] cmd: {' '.join(gomobile_cmd)}")
        os.chdir(libv2ray_dir)
        ret = subprocess.run(gomobile_cmd)
        if ret.returncode != 0:
            raise Exception("gomobile bind failed")

        # Копируем xcframework в lib_dir/ для apple_deploy.
        src_xcf = os.path.join(libv2ray_dir, "Libv2ray.xcframework")
        dst_xcf = os.path.join(self.lib_dir, "Libv2ray.xcframework")
        if not os.path.isdir(src_xcf):
            raise Exception(f"gomobile didn't produce {src_xcf}")
        if os.path.isdir(dst_xcf):
            shutil.rmtree(dst_xcf)
        print(f"[apple_gomobile] cp -R {src_xcf} → {dst_xcf}")
        shutil.copytree(src_xcf, dst_xcf)

        # =====================================================================
        # 2. cgo build libv2ray.a (macOS universal arm64+amd64)
        # =====================================================================
        self._build_macos_libv2ray_a()

        self.after_build()
        self.revert_go_env()

    def _build_macos_libv2ray_a(self):
        """Собирает libv2ray.a universal для macOS из libv2ray_cgo/ subpackage.

        Используется v2ray_flutter/macos podspec через `vendored_libraries`.
        Это статическая библиотека для macOS Flutter (НЕ xcframework).
        """
        cgo_pkg_dir = os.path.join(self.lib_dir, "libv2ray_cgo")
        if not os.path.isdir(cgo_pkg_dir):
            raise Exception(f"libv2ray_cgo/ subpackage not found: {cgo_pkg_dir}")

        macos_out = os.path.join(self.lib_dir, "macos")
        os.makedirs(macos_out, exist_ok=True)

        os.chdir(self.lib_dir)

        # 2026-05-22 MegaV: gomobile bind (выше) делает go mod tidy внутри
        # libv2ray/ — после этого replace для ../xray-core может быть
        # потерян, и cgo build для amd64/arm64 тянет UPSTREAM xray-core
        # без наших патчей (PR #5805, dialer_proxy_fallback_tag).
        # Принудительно пере-init'им go env перед cgo чтобы replace вернулся.
        print("[apple_gomobile] re-init go env before cgo (restore replace=../xray-core)")
        self.init_go_env()
        # init_go_env возвращает chdir в self.lib_dir и оставляет go.mod с
        # replace + go mod tidy. Идём дальше.
        os.chdir(self.lib_dir)

        for arch in ("arm64", "amd64"):
            print(f"[apple_gomobile] cgo build libv2ray.a {arch}...")
            arch_dir = os.path.join(macos_out, arch)
            os.makedirs(arch_dir, exist_ok=True)
            env = os.environ.copy()
            env["CGO_ENABLED"] = "1"
            env["GOOS"] = "darwin"
            env["GOARCH"] = arch
            ret = subprocess.run(
                [
                    "go", "build",
                    "-buildmode=c-archive",
                    "-ldflags=-s -w",
                    "-o", os.path.join(arch_dir, "libv2ray.a"),
                    "./libv2ray_cgo",
                ],
                env=env,
            )
            if ret.returncode != 0:
                raise Exception(f"cgo build libv2ray.a {arch} failed")

        # lipo universal: arm64 + amd64 → одна fat .a
        print("[apple_gomobile] lipo universal arm64+amd64 → macos/libv2ray.a")
        ret = subprocess.run(
            [
                "lipo", "-create",
                os.path.join(macos_out, "arm64", "libv2ray.a"),
                os.path.join(macos_out, "amd64", "libv2ray.a"),
                "-output", os.path.join(macos_out, "libv2ray.a"),
            ]
        )
        if ret.returncode != 0:
            raise Exception("lipo libv2ray.a failed")

        # Header (одинаковый для arm64 и amd64 — берём arm64).
        shutil.copy(
            os.path.join(macos_out, "arm64", "libv2ray.h"),
            os.path.join(macos_out, "libv2ray.h"),
        )

        # Удаляем промежуточные arch-dirs, оставляем только финальные macos/libv2ray.{a,h}
        for arch in ("arm64", "amd64"):
            arch_dir = os.path.join(macos_out, arch)
            if os.path.isdir(arch_dir):
                shutil.rmtree(arch_dir)

        print(f"[apple_gomobile] ✓ macos/libv2ray.a (universal) + libv2ray.h готовы")
