"""
Деплоит свежесобранные Android-артефакты в production-пути проекта MegaV.

★ Production пути:
  - v2ray_flutter/android/libs/libv2ray.aar
        (подключается через flatDir в v2ray_flutter/android/build.gradle:
         flatDir { dirs "$projectDir/libs" } + api(name: 'libv2ray', ext: 'aar'))

Почему один путь:
  Android-target собирается ОДНОЙ Gradle-сборкой (нет аналогичного разделения
  на Runner/Extension как у iOS). v2ray_flutter — единственный consumer
  libv2ray.aar; vpn_native_client тянет его транзитивно как Flutter-плагин.

Зачем sources.jar:
  Не используется в production (Flutter-проекту нужны только бинарники).
  Сохраняется в исходной директории libXray/libv2ray/ для отладки в IDE.

Используется автоматически из `python3 build/main.py android libv2ray`
(см. android_libv2ray.py::after_build).
"""

import os

from app.apple_deploy import _replace_file


_DEPLOY_TARGETS = {
    # gomobile-binding AAR, единый для Flutter Android.
    # 4 ABI внутри: armeabi-v7a, arm64-v8a, x86, x86_64.
    "libv2ray/libv2ray.aar": [
        "v2ray_flutter/android/libs/libv2ray.aar",
    ],
}


def deploy_android_aar(libxray_dir: str):
    """Деплоит свежесобранный libv2ray.aar в production.

    libxray_dir — путь к корню libXray (родитель которой = MegaV root).
    Если артефакта нет (сборка не запускалась/упала раньше) — skip с warning,
    не падаем (позволяет частичной cборке работать).
    """
    megav_root = os.path.abspath(os.path.join(libxray_dir, ".."))
    print(f"[android-deploy] MegaV root: {megav_root}")

    for artifact_name, targets in _DEPLOY_TARGETS.items():
        src = os.path.join(libxray_dir, artifact_name)
        if not os.path.isfile(src):
            print(f"[android-deploy] skip {artifact_name} — not built ({src})")
            continue

        for rel in targets:
            dst = os.path.join(megav_root, rel)
            _replace_file(src, dst)

    print("[android-deploy] OK")
