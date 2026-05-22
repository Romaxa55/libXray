"""
Деплоит свежесобранные Libv2ray-артефакты в production-пути проекта MegaV.

★ Production пути:
  - iOS Runner-app: v2ray_flutter/ios/Frameworks/Libv2ray.xcframework
                    (подключается через `vendored_frameworks` в
                    ios/v2ray_flutter.podspec → CocoaPods для Runner-таргета)
  - iOS Extension:  vpn_native_client/ios/Frameworks/Libv2ray.xcframework
                    (подключается прямой ссылкой в Runner.xcodeproj для
                    MegaVTunnel-таргета; CocoaPods extension'у не даёт
                    v2ray_flutter pod, потому что он тянет Flutter-зависимости,
                    что запрещено в App Extension'ах)
  - macOS:          v2ray_flutter/macos/Classes/libv2ray.a + libv2ray.h
                    (подключается через `vendored_libraries` в
                    macos/v2ray_flutter.podspec)

Почему iOS xcframework лежит в двух местах:
  Runner и MegaVTunnel — отдельные таргеты. Runner получает Libv2ray через
  CocoaPods, MegaVTunnel — через прямую ссылку в pbxproj. Извне (с т.з.
  диска) это две копии, но в Runner.app они сольются в одну: CocoaPods
  кладёт Libv2ray.framework в Runner.app/Frameworks, а extension линкует но
  не embed'ит — берёт фреймворк из host-app bundle через rpath. Дубль на
  диске — цена за разные механизмы интеграции для двух таргетов.

Удалённые legacy-копии:
  - v2ray_flutter/libs/ios/Libv2ray.xcframework — backup, не подключался ни
    одним podspec
  - v2ray_flutter/libs/macos/libv2ray.a — то же самое
  - LibXray.xcframework (cgo) везде — legacy, не использовался ни в Swift,
    ни в podspec, 0 ссылок в pbxproj

Используется из `python3 build/main.py apple all`.
"""

import os
import shutil


# Пути относительно корня MegaV (родитель libXray/).
# Каждый ключ — имя артефакта в libxray_dir/. Значение — production пути.
_DEPLOY_TARGETS = {
    # gomobile xcframework для iOS (device + sim + maccatalyst + macOS-as-iOS)
    # Два пути: один для Runner-app через CocoaPods, второй для
    # MegaVTunnel-extension через прямую ссылку в pbxproj.
    "Libv2ray.xcframework": [
        "v2ray_flutter/ios/Frameworks/Libv2ray.xcframework",
        "vpn_native_client/ios/Frameworks/Libv2ray.xcframework",
    ],
    # cgo static library для macOS Flutter (статический линк в Runner.app)
    "macos/libv2ray.a": [
        "v2ray_flutter/macos/Classes/libv2ray.a",
    ],
    "macos/libv2ray.h": [
        "v2ray_flutter/macos/Classes/libv2ray.h",
    ],
}


def _replace_dir(src: str, dst: str):
    """Атомарная подмена директории через .new + rename."""
    if not os.path.isdir(src):
        raise Exception(f"deploy: source dir not found: {src}")
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    dst_new = dst + ".new"
    if os.path.exists(dst_new):
        shutil.rmtree(dst_new)
    print(f"  → copy {src} → {dst}")
    shutil.copytree(src, dst_new)
    if os.path.exists(dst):
        shutil.rmtree(dst)
    os.rename(dst_new, dst)


def _replace_file(src: str, dst: str):
    """Атомарная подмена файла через .new + rename."""
    if not os.path.isfile(src):
        raise Exception(f"deploy: source file not found: {src}")
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    dst_new = dst + ".new"
    if os.path.exists(dst_new):
        os.remove(dst_new)
    print(f"  → copy {src} → {dst}")
    shutil.copy2(src, dst_new)
    if os.path.exists(dst):
        os.remove(dst)
    os.rename(dst_new, dst)


def deploy_apple_xcframeworks(libxray_dir: str):
    """Деплоит все Apple-артефакты (xcframework + macOS libv2ray.a/.h).

    libxray_dir — путь к корню libXray (родитель которой = MegaV root).
    """
    megav_root = os.path.abspath(os.path.join(libxray_dir, ".."))
    print(f"[deploy] MegaV root: {megav_root}")

    for artifact_name, targets in _DEPLOY_TARGETS.items():
        src = os.path.join(libxray_dir, artifact_name)
        is_dir = artifact_name.endswith(".xcframework")
        exists = os.path.isdir(src) if is_dir else os.path.isfile(src)

        if not exists:
            # Если артефакт не собран в этой сессии — пропускаем, не падаем.
            # Это позволяет частичной сборке (например только iOS) работать.
            print(f"[deploy] skip {artifact_name} — not built ({src})")
            continue

        for rel in targets:
            dst = os.path.join(megav_root, rel)
            if is_dir:
                _replace_dir(src, dst)
            else:
                _replace_file(src, dst)

    print("[deploy] OK")
