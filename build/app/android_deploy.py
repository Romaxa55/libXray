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
import shutil
import subprocess

from app.apple_deploy import _replace_file

# ★ Android CI качает libv2ray.aar НЕ из репо, а из этого GitHub Release
# (см. .github/workflows/build-android.yml «Download libv2ray.aar from Release»).
# Поэтому каждая пересборка ОБЯЗАНА синхронизировать сюда — иначе CI соберёт
# со старой нативкой. Инцидент 2026-06-04: рассинхрон → release не стартует xray.
_AAR_RELEASE_TAG = "windows-deps"
_AAR_RELEASE_REPO = "Romaxa55/MegaV_Public"


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

    _upload_aar_to_release(megav_root)
    print("[android-deploy] OK")


def _upload_aar_to_release(megav_root: str):
    """★ Авто-синхронизация libv2ray.aar с GitHub Release windows-deps.

    КРИТИЧНО (инцидент 2026-06-04): Android CI качает aar из Release, НЕ из
    репо. Без этой синхронизации CI собирает со СТАРОЙ нативкой → release-сборка
    не поднимает xray (debug/локально работает на репо-aar, прод — мёртв).
    Эта функция делает «при сборке либы — пушим в Release» автоматически.

    Best-effort: если gh нет/не авторизован — ГРОМКИЙ warning, но НЕ падаем
    (локальная сборка не должна ломаться из-за отсутствия gh-токена; зато
    разработчик увидит, что Release остался старым и CI соберёт битьё).
    """
    aar = os.path.join(megav_root, "v2ray_flutter/android/libs/libv2ray.aar")
    if not os.path.isfile(aar):
        print("[android-deploy] upload skip — aar отсутствует")
        return
    if shutil.which("gh") is None:
        print("[android-deploy] ⚠️⚠️ gh CLI не найден — Release "
              f"{_AAR_RELEASE_TAG} НЕ ОБНОВЛЁН! CI соберёт со СТАРЫМ aar.\n"
              f"    Залей вручную: gh release upload {_AAR_RELEASE_TAG} "
              f"{aar} -R {_AAR_RELEASE_REPO} --clobber")
        return
    print(f"[android-deploy] uploading libv2ray.aar → Release "
          f"{_AAR_RELEASE_TAG} ({_AAR_RELEASE_REPO})...")
    try:
        subprocess.run(
            ["gh", "release", "upload", _AAR_RELEASE_TAG, aar,
             "-R", _AAR_RELEASE_REPO, "--clobber"],
            check=True,
        )
        print("[android-deploy] ✅ Release синхронизирован — CI возьмёт свежий aar")
    except subprocess.CalledProcessError as exc:
        print(f"[android-deploy] ⚠️⚠️ gh release upload FAILED ({exc}) — Release "
              f"{_AAR_RELEASE_TAG} НЕ ОБНОВЛЁН! CI соберёт со СТАРЫМ aar. "
              "Залей вручную.")
