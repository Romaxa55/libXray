import os.path
import subprocess

from app.build import Builder
from app.cmd import create_dir_if_not_exists, delete_dir_if_exists


class WindowsBuilder(Builder):
    def __init__(self, build_dir: str):
        super().__init__(build_dir)
        self.framework_dir = os.path.join(self.lib_dir, "windows_dll")
        delete_dir_if_exists(self.framework_dir)
        create_dir_if_not_exists(self.framework_dir)
        # 2026-06-04 MegaV FIX: Windows app (vpn_native_client/windows/runner/
        # vpn_manager.cpp) грузит `libv2ray.dll` и резолвит символы
        # InitializeV2Ray / StartV2RayWithConfig / StopV2Ray / IsV2RayRunning /
        # GetV2RayVersion / CleanupV2Ray / Free. Эти символы экспортит пакет
        # `libv2ray_cgo` (тот же, что macOS libv2ray.a), а НЕ корневой пакет
        # libXray (он даёт CGo*-API: CGoRunXrayFromJSON и т.п., который
        # приложение никогда не грузило). Старый windows.py собирал КОРЕНЬ →
        # libXray.dll с CGo*-символами = битый артефакт под другим именем.
        # Теперь собираем правильный пакет ./libv2ray_cgo как c-shared →
        # libv2ray.dll (по аналогии с macOS _build_macos_libv2ray_a).
        self.lib_file = "libv2ray.dll"
        self.lib_header_file = "libv2ray.h"
        self.bin_file = "xray.exe"

    def before_build(self):
        # Только go env: replace=../xray-core (форк с PR #5805 + defuse
        # date-timebomb) + timebomb-guard + go mod tidy.
        #
        # НЕ зовём prepare_static_lib(): он копирует main.gotemplate в корень
        # и переименовывает `package libXray` → `package main`, что ломает
        # `import "github.com/xtls/libxray"` ВНУТРИ libv2ray_cgo (нельзя
        # импортировать package main). libv2ray_cgo сам уже `package main`
        # с пустым func main() — его и собираем.
        #
        # НЕ зовём download_geo(): geoip.dat/geosite.dat нужны xray в РАНТАЙМЕ,
        # а не при компиляции DLL. Их бандлит сам Flutter Windows-инсталлятор.
        self.init_go_env()

    def build(self):
        self.before_build()
        self.build_windows()
        self.after_build()

    def build_windows(self):
        output_dir = self.framework_dir
        create_dir_if_not_exists(output_dir)
        output_file = os.path.join(output_dir, self.lib_file)
        run_env = os.environ.copy()
        run_env["CGO_ENABLED"] = "1"
        # CC/CXX берём из окружения если заданы (на vn-windows — полный путь
        # к mingw gcc), иначе дефолт gcc.exe/g++.exe из PATH.
        run_env.setdefault("CC", "gcc.exe")
        run_env.setdefault("CXX", "g++.exe")

        cmd = [
            "go",
            "build",
            "-trimpath",
            "-ldflags",
            "-s -w",
            f"-o={output_file}",
            "-buildmode=c-shared",
            "./libv2ray_cgo",
        ]
        os.chdir(self.lib_dir)
        print(cmd)
        ret = subprocess.run(cmd, env=run_env)
        if ret.returncode != 0:
            raise Exception("build_windows (libv2ray_cgo c-shared) failed")

    def after_build(self):
        # Корень НЕ мутировали (prepare_static_lib не звали), reset_files не
        # нужен. go.mod с replace=../xray-core оставляем активным.
        pass
