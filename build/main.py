# main.py
import os
import sys

from app.android import AndroidBuilder
from app.android_libv2ray import AndroidLibV2RayBuilder
from app.apple_go import AppleGoBuilder
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
        tool = sys.argv[2]
        if tool == "go":
            builder = AppleGoBuilder(build_dir_path())
            builder.build()
        elif tool == "gomobile":
            builder = AppleGoMobileBuilder(build_dir_path())
            builder.build()
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
