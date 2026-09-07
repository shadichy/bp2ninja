#!/usr/bin/env python3
"""
generate_aur_pkgbuilds.py: Automated AUR PKGBUILD generator for bp2ninja and custom plugins.
"""

import os
from pathlib import Path

BP2NINJA_DIR = Path(__file__).resolve().parent.parent
AUR_DIR = BP2NINJA_DIR.parent.parent / "aur"
AUR_DIR.mkdir(parents=True, exist_ok=True)

PACKAGES = [
    {
        'name': 'aidl-soong-rules',
        'desc': 'bp2ninja plugin for Android AIDL Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['aidl-soong-rules-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/system/tools/aidl/+archive/android-14.0.0_r67/build.tar.gz'],
    },
    {
        'name': 'cuttlefish-soong-rules',
        'desc': 'bp2ninja plugin for Cuttlefish virtual device Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['cuttlefish-soong-rules-14.0.0_r67.tar.gz::https://android.googlesource.com/device/google/cuttlefish/+archive/android-14.0.0_r67/build.tar.gz'],
    },
    {
        'name': 'hidl-soong-rules',
        'desc': 'bp2ninja plugin for Android HIDL Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['hidl-soong-rules-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/system/tools/hidl/+archive/android-14.0.0_r67/build.tar.gz'],
    },
    {
        'name': 'kernel-config-soong-rules',
        'desc': 'bp2ninja plugin for Android Kernel Config Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['kernel-config-soong-rules-main.tar.gz::https://android.googlesource.com/kernel/configs/+archive/main/build.tar.gz'],
    },
    {
        'name': 'soong-api',
        'desc': 'bp2ninja plugin for Android OS Frameworks Base API Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-api-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/frameworks/base/+archive/android-14.0.0_r67/api.tar.gz'],
    },
    {
        'name': 'soong-art',
        'desc': 'bp2ninja plugin for Android Runtime (ART) Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-art-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/art/+archive/android-14.0.0_r67/build.tar.gz'],
    },
    {
        'name': 'soong-ca-certificates',
        'desc': 'bp2ninja plugin for Android CA Certificates Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-ca-certificates-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/system/ca-certificates/+archive/android-14.0.0_r67/soong.tar.gz'],
    },
    {
        'name': 'soong-ca-certificates-apex',
        'desc': 'bp2ninja plugin for Conscrypt CA Certificates APEX Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-ca-certificates-apex-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/external/conscrypt/+archive/android-14.0.0_r67/apex/ca-certificates/soong.tar.gz'],
    },
    {
        'name': 'soong-clang',
        'desc': 'bp2ninja plugin for Clang TableGen Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-clang-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/external/clang/+archive/android-14.0.0_r67/soong.tar.gz'],
    },
    {
        'name': 'soong-clang-prebuilts',
        'desc': 'bp2ninja plugin for Clang Host Prebuilts Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-clang-prebuilts-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/prebuilts/clang/host/linux-x86/+archive/android-14.0.0_r67/soong.tar.gz'],
    },
    {
        'name': 'soong-fs_config',
        'desc': 'bp2ninja plugin for Android build fs_config Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-fs_config-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/build/+archive/android-14.0.0_r67/tools/fs_config.tar.gz'],
    },
    {
        'name': 'soong-libchrome',
        'desc': 'bp2ninja plugin for libchrome Mojom codegen Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-libchrome-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/external/libchrome/+archive/android-14.0.0_r67/soong.tar.gz'],
    },
    {
        'name': 'soong-llvm',
        'desc': 'bp2ninja plugin for LLVM TableGen Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-llvm-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/external/llvm/+archive/android-14.0.0_r67/soong.tar.gz'],
    },
    {
        'name': 'soong-llvm22',
        'desc': 'bp2ninja plugin for LLVM 22 TableGen Soong rules',
        'ver': '22.0.0',
        'sources': [
            'Android.bp::https://raw.githubusercontent.com/android-generic/external_llvm-project/release_22.x/llvm/soong/Android.bp',
            'llvm.go::https://raw.githubusercontent.com/android-generic/external_llvm-project/release_22.x/llvm/soong/llvm.go',
            'min_tblgen.go::https://raw.githubusercontent.com/android-generic/external_llvm-project/release_22.x/llvm/soong/min_tblgen.go',
            'tblgen.go::https://raw.githubusercontent.com/android-generic/external_llvm-project/release_22.x/llvm/soong/tblgen.go'
        ],
    },
    {
        'name': 'soong-robolectric',
        'desc': 'bp2ninja plugin for Robolectric build props Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-robolectric-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/external/robolectric/+archive/android-14.0.0_r67/soong.tar.gz'],
    },
    {
        'name': 'soong-rust-prebuilts',
        'desc': 'bp2ninja plugin for Rust Host Prebuilts Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-rust-prebuilts-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/prebuilts/rust/+archive/android-14.0.0_r67/soong.tar.gz'],
    },
    {
        'name': 'soong-selinux',
        'desc': 'bp2ninja plugin for SELinux / Sepolicy Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-selinux-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/system/sepolicy/+archive/android-14.0.0_r67/build/soong.tar.gz'],
    },
    {
        'name': 'soong-wayland-protocol-codegen',
        'desc': 'bp2ninja plugin for Wayland protocol codegen Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['soong-wayland-protocol-codegen-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/external/wayland-protocols/+archive/android-14.0.0_r67.tar.gz'],
    },
    {
        'name': 'vintf-compatibility-matrix-soong-rules',
        'desc': 'bp2ninja plugin for VINTF compatibility matrix Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['vintf-compatibility-matrix-soong-rules-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/hardware/interfaces/+archive/android-14.0.0_r67/compatibility_matrices/build.tar.gz'],
    },
    {
        'name': 'xsdc-soong-rules',
        'desc': 'bp2ninja plugin for XSDC XML schema compiler Soong rules',
        'ver': '14.0.0_r67',
        'sources': ['xsdc-soong-rules-14.0.0_r67.tar.gz::https://android.googlesource.com/platform/system/tools/xsdc/+archive/android-14.0.0_r67/build.tar.gz'],
    },
]

def main():
    print(f"Generating PKGBUILDs in: {AUR_DIR}")
    for p in PACKAGES:
        folder = AUR_DIR / f"bp2ninja-plugin-{p['name']}"
        folder.mkdir(parents=True, exist_ok=True)
        
        src_lines = "\n    ".join(f'"{s}"' for s in p['sources'])
        sha_lines = "\n    ".join("'SKIP'" for _ in p['sources'])
        
        content = f"""# Maintainer: Android Generic / Bliss OS Team
pkgname=bp2ninja-plugin-{p['name']}
_plugin_name={p['name']}
pkgver={p['ver']}
pkgrel=1
pkgdesc="{p['desc']}"
arch=('x86_64' 'aarch64')
license=('Apache-2.0')
depends=('bp2ninja')
makedepends=('go')
provides=("{p['name']}" "bp2ninja-plugin-{p['name']}")
conflicts=("{p['name']}")
source=(
    {src_lines}
)
sha256sums=(
    {sha_lines}
)

build() {{
    cd "$srcdir"
    bp2ninja convert-plugin . -o "${{_plugin_name}}.so"
}}

package() {{
    install -Dm755 "$srcdir/${{_plugin_name}}.so" "$pkgdir/usr/lib/bp2ninja/plugins/${{_plugin_name}}.so"
}}
"""
        pkgbuild_path = folder / "PKGBUILD"
        pkgbuild_path.write_text(content, encoding="utf-8")
        print(f"  [+] {folder.name}/PKGBUILD")

    print(f"\n[OK] Generated {len(PACKAGES)} plugin PKGBUILDs successfully.")

if __name__ == "__main__":
    main()
