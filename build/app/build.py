import os.path
import re
import shutil
import subprocess

from app.cmd import (
    create_dir_if_not_exists,
    delete_file_if_exists,
    delete_dir_if_exists,
)

LIBXRAY_MOD_NAME = "github.com/xtls/libxray"


class Builder(object):
    def __init__(self, build_dir: str):
        self.build_dir = build_dir
        self.lib_dir = os.path.join(self.build_dir, "..")
        self.bin_file = "xray"

    def clean_lib_files(self, files: list[str]):
        for file in files:
            file_path = os.path.join(self.lib_dir, file)
            delete_file_if_exists(file_path)

    def clean_lib_dirs(self, dirs: list[str]):
        for dir_name in dirs:
            dir_path = os.path.join(self.lib_dir, dir_name)
            delete_dir_if_exists(dir_path)

    def init_go_env(self):
        os.chdir(self.lib_dir)
        self.clean_lib_files(["go.mod", "go.sum"])
        ret = subprocess.run(["go", "mod", "init", LIBXRAY_MOD_NAME])
        if ret.returncode != 0:
            raise Exception("go mod init failed")

        # MegaV-patch 2026-05-18: pin xray-core на v26.5.9 (с XHTTP leak fix).
        # Без этого pin'а go mod tidy берёт @latest = v1.260327.0 без common/geodata,
        # build падает с "module does not contain package geodata".
        ret = subprocess.run(
            [
                "go",
                "get",
                "github.com/xtls/xray-core@1bdb488c9ec09ea51e6899697d5b7437f3cf6eb2",
            ]
        )
        if ret.returncode != 0:
            raise Exception("go get xray-core pinned commit failed")

        # 2026-05-20: replace на ЛОКАЛЬНЫЙ форк xray-core.
        # Папка `../xray-core` относительно `libXray/` — это ПОСТОЯННЫЙ git-форк
        # (branch feature/dialer-proxy-balancer), клонируется вручную (см. .gitignore).
        # Все наши доработки лежат КОММИТАМИ в дереве форка, а не накатываются
        # патчами при сборке:
        #   - PR #5805 (резолв balancer-tag в sockopt.dialerProxy, chain-mode VPN)
        #   - dialerProxyFallbackTag, route-notification hook, memory-fix
        #   - 2026-06-02 b879ebd1: degrade allowInsecure date-fatal → warning,
        #     hysteria OmitMaxDatagramFrameSize pin, browser.go uTLS re-anchor.
        # Сборка НЕ делает `go get` на upstream и НЕ ресетит дерево форка — оно
        # стабильно между сборками. patches/*.patch оставлены как backup-diff
        # для восстановления, если форк когда-нибудь снесут и зальют заново.
        ret = subprocess.run(
            ["go", "mod", "edit", "-replace=github.com/xtls/xray-core=../xray-core"]
        )
        if ret.returncode != 0:
            raise Exception("go mod edit -replace ../xray-core failed")

        # 2026-06-02: time-bomb sanity-guard. Degrade уже в дереве форка (b879ebd1),
        # так что в норме эта проверка просто проходит. Она ловит РЕГРЕСС — если
        # кто-то случайно сбросит ../xray-core на чистый upstream (ручной `go get`
        # или git reset), date-gated фатал на allowInsecure вернётся и тихо
        # положит каждого выпущенного клиента (xray stopped, SOCKS refused,
        # "сайты не открываются"). Лучше упасть на сборке, чем выпустить такой
        # libv2ray. Восстановление: вернуть форк на feature/dialer-proxy-balancer
        # или, в крайнем случае, накатить patches/0002-defuse-date-timebombs.patch.
        guard = os.path.join(self.lib_dir, "scripts", "check-no-timebomb.sh")
        ret = subprocess.run(["bash", guard])
        if ret.returncode != 0:
            raise Exception(
                "time-bomb guard failed: date-gated fatal/flip returned in "
                "../xray-core — re-apply patches/0002-defuse-date-timebombs.patch"
            )

        ret = subprocess.run(
            [
                "go",
                "mod",
                "tidy",
            ]
        )
        if ret.returncode != 0:
            raise Exception("go mod tidy failed")

    def download_geo(self):
        os.chdir(self.lib_dir)
        # MegaV-patch 2026-05-18: пропускаем download если файлы есть.
        # download_geo/main.go импортирует root libxray (package main), что
        # ломается если в Go-кэше нет уже скомпилированных артефактов.
        geoip = os.path.join(self.lib_dir, "dat", "geoip.dat")
        geosite = os.path.join(self.lib_dir, "dat", "geosite.dat")
        if os.path.exists(geoip) and os.path.exists(geosite):
            print(f"[download_geo] skip — files already exist in {os.path.dirname(geoip)}")
            return
        main_path = os.path.join("download_geo", "main.go")
        ret = subprocess.run(["go", "run", main_path])
        if ret.returncode != 0:
            raise Exception("download_geo failed")

    def prepare_gomobile(self):
        ret = subprocess.run(
            ["go", "install", "golang.org/x/mobile/cmd/gomobile@latest"]
        )
        if ret.returncode != 0:
            raise Exception("go install gomobile failed")
        ret = subprocess.run(["gomobile", "init"])
        if ret.returncode != 0:
            raise Exception("gomobile init failed")
        ret = subprocess.run(["go", "get", "golang.org/x/mobile/cmd/gomobile"])
        if ret.returncode != 0:
            raise Exception("gomobile update failed")
        ret = subprocess.run(["go", "get", "google.golang.org/genproto"])
        if ret.returncode != 0:
            raise Exception("gomobile install genproto failed")

    def prepare_static_lib(self):
        self.copy_template_file()
        self.fix_package_name()

    def copy_template_file(self):
        src_file = os.path.join(self.build_dir, "template", "main.gotemplate")
        dst_file = os.path.join(self.lib_dir, "main.go")
        shutil.copy(src_file, dst_file)

    def fix_package_name(self):
        files = os.listdir(self.lib_dir)
        for file in files:
            if file.endswith(".go"):
                self.replace_package_name(file)

    def replace_package_name(self, file_name: str):
        file_path = os.path.join(self.lib_dir, file_name)
        new_lines = []
        with open(file_path, "r") as f:
            lines = f.readlines()
            for line in lines:
                new_line = line
                if re.match(r"^package\s+libXray", line):
                    new_line = "package main\n"
                new_lines.append(new_line)
        with open(file_path, "w") as f:
            f.writelines(new_lines)

    def before_build(self):
        self.init_go_env()
        self.download_geo()

    def build(self):
        pass

    def after_build(self):
        pass

    def reset_files(self):
        self.clean_lib_files(["main.go"])
        files = os.listdir(self.lib_dir)
        for file in files:
            if file.endswith(".go"):
                self.reset_package_name(file)

    def reset_package_name(self, file_name: str):
        file_path = os.path.join(self.lib_dir, file_name)
        new_lines = []
        with open(file_path, "r") as f:
            lines = f.readlines()
            for line in lines:
                new_line = line
                if re.match(r"^package\s+main", line):
                    new_line = "package libXray\n"
                new_lines.append(new_line)
        with open(file_path, "w") as f:
            f.writelines(new_lines)

    def build_desktop_bin(self):
        bin_dir = os.path.join(self.lib_dir, "bin")
        create_dir_if_not_exists(bin_dir)
        output_file = os.path.join(bin_dir, self.bin_file)
        run_env = os.environ.copy()
        run_env["CGO_ENABLED"] = "0"

        cmd = [
            "go",
            "build",
            "-trimpath",
            "-ldflags",
            "-s -w",
            f"-o={output_file}",
            "./desktop_bin",
        ]
        os.chdir(self.lib_dir)
        print(cmd)
        ret = subprocess.run(cmd, env=run_env)
        if ret.returncode != 0:
            raise Exception(f"build_desktop_bin failed")

    def revert_go_env(self):
        """No-op (2026-05-22 MegaV): раньше после сборки эта функция
        сбрасывала go.mod на upstream xray-core (без `replace=../xray-core`).
        Это создавало проблемы: следующая команда сборки (apple_gomobile cgo
        для другой архитектуры, или ad-hoc `go build ./desktop_bin`)
        собиралась с UPSTREAM xray-core — без наших патчей (PR #5805,
        dialer_proxy_fallback_tag, SetRouteNotifier).

        Юзер 2026-05-22: «убери нахуй пусть всегда идёт через нашу форк».
        Теперь go.mod ВСЕГДА содержит `replace=../xray-core` после
        `init_go_env()`. Никаких revert'ов. Это правильно — форк это
        единственный авторитетный источник, IDE/dev-tools тоже видят
        наш форк (это _фича_, не баг).

        Если когда-нибудь форк удалят и нужен будет upstream — это можно
        сделать вручную через `go mod edit -dropreplace=github.com/xtls/xray-core`.
        """
        print("[build] revert_go_env: no-op (replace=../xray-core stays active)")
