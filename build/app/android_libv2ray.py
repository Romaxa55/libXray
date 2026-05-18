# app/android_libv2ray.py
#
# Сборщик compat-shim AAR для Android из подпакета libXray/libv2ray/.
#
# Что собирает:
#   gomobile bind подпакета github.com/xtls/libxray/libv2ray (package libv2ray)
#   → libv2ray.aar + libv2ray-sources.jar (4 ABI: armeabi-v7a, arm64-v8a, x86, x86_64).
#
# Чем отличается от AndroidBuilder:
#   AndroidBuilder собирает root-пакет libXray (libXray.aar) — gomobile bind .
#   из корня репо. Здесь — gomobile bind ./libv2ray, отдельный target package,
#   отдельный AAR. Оба AAR содержат gomobile-runtime (libgojni.so) и в одном
#   APK конфликтуют → выбирается ОДИН, согласно проектной решёнке MegaV:
#   compat-shim libv2ray.aar — единственный для всех мобилок (iOS/macOS/Android),
#   потому что API совпадает с историческим v2ray-flutter binding'ом.
#
# Использование:
#   cd libXray
#   python3 build/main.py android libv2ray
#
# Результат:
#   libXray/libv2ray/libv2ray.aar           (~93 МБ)
#   libXray/libv2ray/libv2ray-sources.jar   (~7 КБ)
#
# Окружение:
#   - Go ≥ 1.24
#   - ANDROID_HOME / ANDROID_SDK_ROOT (gomobile сам найдёт sdkmanager)
#   - ANDROID_NDK_HOME — путь к NDK (рекомендуется 27.x; 28.x тоже работает,
#     но 27 стабильнее под gomobile в 2026-05)
#
# История:
#   2026-05-18: первая ручная сборка дала AAR sha ac39d414 (с ProbeOutbound).
#               До этого в репо лежал AAR sha 7d7037f5 (50 МБ) — собранный
#               ДО добавления ProbeOutbound в libv2ray.go → линкер-эррор
#               в Kotlin. Этот builder делает сборку воспроизводимой в CI.

import os
import subprocess

from app.build import Builder


class AndroidLibV2RayBuilder(Builder):
    """Собирает compat-shim libv2ray.aar для Android."""

    def __init__(self, build_dir: str):
        super().__init__(build_dir)
        # Переопределяем lib_dir на подкаталог libv2ray/ — gomobile bind
        # берёт текущую директорию как target package (resolve через go.mod
        # родительского libXray модуля).
        self.libv2ray_dir = os.path.join(self.lib_dir, "libv2ray")

    def before_build(self):
        # Не вызываем super().before_build() — init_go_env / download_geo
        # делаются один раз в корне libXray (если идёт многотарget билд,
        # AndroidBuilder это сделает первым). Здесь просто гарантируем,
        # что gomobile установлен.
        self.prepare_gomobile()

    def build(self):
        self.before_build()

        clean_files = ["libv2ray.aar", "libv2ray-sources.jar"]
        for f in clean_files:
            path = os.path.join(self.libv2ray_dir, f)
            if os.path.exists(path):
                os.remove(path)

        os.chdir(self.libv2ray_dir)
        ret = subprocess.run(
            [
                "gomobile",
                "bind",
                "-target",
                "android",
                "-androidapi",
                "21",
                "-ldflags=-extldflags=-Wl,-z,max-page-size=16384",
            ]
        )
        if ret.returncode != 0:
            raise Exception("android-libv2ray build failed")

        self.after_build()

        self.revert_go_env()
