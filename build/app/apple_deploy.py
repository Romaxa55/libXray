"""
Копирует свежесобранные Libv2ray.xcframework и LibXray.xcframework в
проект MegaV — те места, откуда их читает iOS/macOS-сборка Flutter-приложения.

Цели копирования:
  - vpn_native_client/ios/Frameworks/Libv2ray.xcframework
  - vpn_native_client/ios/Frameworks/LibXray.xcframework
  - v2ray_flutter/ios/Frameworks/Libv2ray.xcframework
  - v2ray_flutter/ios/Frameworks/LibXray.xcframework
  - v2ray_flutter/libs/ios/Libv2ray.xcframework

Это устраняет ручное `cp -R` после каждой пересборки. Используется из
команды `python3 build/main.py apple all`.
"""

import os
import shutil

# Все пути относительно корня MegaV (родитель libXray/).
_RELATIVE_DEPLOY_TARGETS = {
    "Libv2ray.xcframework": [
        "vpn_native_client/ios/Frameworks/Libv2ray.xcframework",
        "v2ray_flutter/ios/Frameworks/Libv2ray.xcframework",
        "v2ray_flutter/libs/ios/Libv2ray.xcframework",
    ],
    "LibXray.xcframework": [
        "vpn_native_client/ios/Frameworks/LibXray.xcframework",
        "v2ray_flutter/ios/Frameworks/LibXray.xcframework",
    ],
}


def _replace_xcframework(src: str, dst: str):
    """Атомарная подмена: пишем в dst.new, потом mv. Защита от частичного
    копирования если процесс упадёт посреди."""
    if not os.path.isdir(src):
        raise Exception(f"deploy: source not found: {src}")

    os.makedirs(os.path.dirname(dst), exist_ok=True)
    dst_new = dst + ".new"

    # Очистка предыдущей попытки если осталась.
    if os.path.exists(dst_new):
        shutil.rmtree(dst_new)

    print(f"  → copy {src} → {dst}")
    shutil.copytree(src, dst_new)

    # Удаляем старый и переименовываем новый.
    if os.path.exists(dst):
        shutil.rmtree(dst)
    os.rename(dst_new, dst)


def deploy_apple_xcframeworks(libxray_dir: str):
    """libxray_dir — путь к корню libXray (так, что rel'ы выше работают
    как `libxray_dir/../{path}`)."""
    megav_root = os.path.abspath(os.path.join(libxray_dir, ".."))
    print(f"[deploy] MegaV root: {megav_root}")

    for framework_name, targets in _RELATIVE_DEPLOY_TARGETS.items():
        src = os.path.join(libxray_dir, framework_name)
        if not os.path.isdir(src):
            # Если xcframework не пересобран в этой сессии — пропустим.
            # Это позволяет `apple all` работать даже если упал go-target.
            print(f"[deploy] skip {framework_name} — not built ({src})")
            continue

        for rel in targets:
            dst = os.path.join(megav_root, rel)
            _replace_xcframework(src, dst)

    print("[deploy] OK")
