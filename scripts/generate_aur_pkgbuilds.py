#!/usr/bin/env python3
"""
generate_aur_pkgbuilds.py: Automated AUR PKGBUILD generator for bp2ninja and custom plugins.
"""

import argparse
import hashlib
import os
from pathlib import Path
import subprocess
import sys

BP2NINJA_DIR = Path(__file__).resolve().parent.parent
DEFAULT_AUR_DIR = BP2NINJA_DIR.parent.parent / "aur_backup"

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

def generate_bp2ninja(target_dir: Path):
    folder = target_dir / "bp2ninja"
    folder.mkdir(parents=True, exist_ok=True)
    pkgver = "0.1.0"
    tar_name = f"bp2ninja-{pkgver}.tar.gz"
    tar_path = folder / tar_name

    # Create tar.gz archive from git HEAD
    cmd = [
        "git", "-C", str(BP2NINJA_DIR), "archive",
        "--format=tar.gz", f"--prefix=bp2ninja-{pkgver}/", "HEAD",
        "-o", str(tar_path)
    ]
    subprocess.run(cmd, check=True)

    # Compute sha256
    with open(tar_path, "rb") as f:
        sha256 = hashlib.sha256(f.read()).hexdigest()

    content = f"""# Maintainer: Android Generic / Bliss OS Team
pkgname=bp2ninja
pkgver={pkgver}
pkgrel=1
pkgdesc="Universal Blueprint (Android.bp) to Ninja generator"
arch=('x86_64' 'aarch64')
license=('Apache-2.0')
depends=('glibc')
makedepends=('go')
optdepends=(
    'ninja: to execute generated build.ninja files'
    'android-ndk: for cross-compiling Android components against Bionic libc'
    'android-ndk-beta: for testing preview Android NDK toolchain releases'
)
source=("{tar_name}")
sha256sums=('{sha256}')

build() {{
    [ -d "${{srcdir}}/${{pkgname}}-${{pkgver}}" ] && cd "${{srcdir}}/${{pkgname}}-${{pkgver}}" || cd "${{srcdir}}/${{pkgname}}"
    export CGO_ENABLED=1
    go build -trimpath -ldflags="-s -w" -o bin/bp2ninja ./cmd/bp2ninja
}}

check() {{
    [ -d "${{srcdir}}/${{pkgname}}-${{pkgver}}" ] && cd "${{srcdir}}/${{pkgname}}-${{pkgver}}" || cd "${{srcdir}}/${{pkgname}}"
    go test -v ./...
}}

package() {{
    [ -d "${{srcdir}}/${{pkgname}}-${{pkgver}}" ] && cd "${{srcdir}}/${{pkgname}}-${{pkgver}}" || cd "${{srcdir}}/${{pkgname}}"

    # Install main binary
    install -Dm755 bin/bp2ninja "${{pkgdir}}/usr/bin/bp2ninja"

    # Install Go module packages and go.mod so plugins can link against them
    install -Dm644 go.mod "${{pkgdir}}/usr/share/bp2ninja/go.mod"
    install -d "${{pkgdir}}/usr/share/bp2ninja/pkg"
    cp -a pkg/* "${{pkgdir}}/usr/share/bp2ninja/pkg/"

    # Create system plugins directory
    install -d "${{pkgdir}}/usr/lib/bp2ninja/plugins"

    # Install documentation
    [ -f README.md ] && install -Dm644 README.md "${{pkgdir}}/usr/share/doc/${{pkgname}}/README.md" || true
}}
"""
    (folder / "PKGBUILD").write_text(content, encoding="utf-8")
    print(f"  [+] {folder.name}/PKGBUILD (sha256: {sha256[:16]}...)")


def generate_plugins(target_dir: Path):
    for p in PACKAGES:
        folder = target_dir / f"bp2ninja-plugin-{p['name']}"
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


def main():
    parser = argparse.ArgumentParser(description="Generate AUR PKGBUILDs for bp2ninja and plugins")
    parser.add_argument("--out-dir", "-o", default=str(DEFAULT_AUR_DIR),
                        help="Target output directory (default: ../../aur_backup)")
    args = parser.parse_args()

    target_dir = Path(args.out_dir).resolve()
    target_dir.mkdir(parents=True, exist_ok=True)
    print(f"Generating PKGBUILDs in: {target_dir}")

    print("\n[*] 1. Generating bp2ninja base package:")
    generate_bp2ninja(target_dir)

    print(f"\n[*] 2. Generating {len(PACKAGES)} plugin packages:")
    generate_plugins(target_dir)

    print(f"\n[OK] Successfully generated bp2ninja and {len(PACKAGES)} plugins in {target_dir}")


if __name__ == "__main__":
    main()
