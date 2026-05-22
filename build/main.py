# main.py
import os
import sys

from app.android import AndroidBuilder
from app.android_libv2ray import AndroidLibV2RayBuilder
from app.apple_deploy import deploy_apple_xcframeworks
from app.apple_gomobile import AppleGoMobileBuilder
from app.linux import LinuxBuilder
from app.windows import WindowsBuilder


def build_dir_path():
    file_dir = os.path.dirname(__file__)
    dir_path = os.path.abspath(file_dir)
    return dir_path


if __name__ == "__main__":
    print(sys.argv)
    platform = sys.argv[1]

    if platform == "apple":
        # python3 build/main.py apple gomobile   → Libv2ray.xcframework (gomobile)
        # python3 build/main.py apple all        → Libv2ray.xcframework + deploy
        #
        # 2026-05-19: УБРАН apple go (cgo). LibXray.xcframework (cgo) был
        # legacy-мусором — нигде не импортировался в Swift, 0 ссылок в pbxproj
        # (vpn_native_client + macos), не в podspec. v2ray_flutter подключает
        # только Libv2ray.xcframework через `vendored_frameworks`. cgo-сборка
        # регулярно падала на Go 1.25+ thin-archive vs xcodebuild — фиксить
        # бессмысленно, потому что результат никому не нужен.
        tool = sys.argv[2]
        if tool == "gomobile" or tool == "all":
            AppleGoMobileBuilder(build_dir_path()).build()
            # Подкладываем свежий xcframework в vpn_native_client + v2ray_flutter.
            # После этого `flutter run -d macos` без ручного cp -R увидит
            # новый xray.
            libxray_dir = os.path.abspath(
                os.path.join(build_dir_path(), "..")
            )
            deploy_apple_xcframeworks(libxray_dir)
        elif tool == "go":
            raise Exception(
                "apple go (cgo / LibXray.xcframework) removed — legacy unused. "
                "Use 'apple all' or 'apple gomobile' (gomobile / Libv2ray.xcframework)."
            )
        else:
            raise Exception(f"platform {platform} tool {tool} not supported")

    elif platform == "android":
        # python3 build/main.py android            → libXray.aar (legacy alias)
        # python3 build/main.py android libxray    → libXray.aar (root package)
        # python3 build/main.py android libv2ray   → libv2ray.aar (compat-shim)
        # python3 build/main.py android all        → оба AAR последовательно
        tool = sys.argv[2] if len(sys.argv) > 2 else "libxray"
        if tool == "libxray":
            builder = AndroidBuilder(build_dir_path())
            builder.build()
        elif tool == "libv2ray":
            builder = AndroidLibV2RayBuilder(build_dir_path())
            builder.build()
        elif tool == "all":
            AndroidBuilder(build_dir_path()).build()
            AndroidLibV2RayBuilder(build_dir_path()).build()
        else:
            raise Exception(f"platform {platform} tool {tool} not supported")

    elif platform == "linux":
        builder = LinuxBuilder(build_dir_path())
        builder.build()

    elif platform == "windows":
        builder = WindowsBuilder(build_dir_path())
        builder.build()

    else:
        raise Exception(f"platform {platform} not supported")
