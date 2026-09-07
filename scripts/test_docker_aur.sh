#!/usr/bin/env bash
set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BP2NINJA_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
AUR_DIR="$(cd "$BP2NINJA_DIR/../../aur" && pwd)"

echo "================================================================="
echo "   bp2ninja AUR Out-of-Tree (Remote Mode) Docker Test Harness    "
echo "================================================================="
echo "AUR Directory: $AUR_DIR"

if [ ! -d "$AUR_DIR/bp2ninja" ]; then
    echo "[-] Error: $AUR_DIR/bp2ninja not found. Run scripts/generate_aur_pkgbuilds.py first."
    exit 1
fi

CONTAINER_NAME="bp2ninja_aur_test_$$"

cleanup() {
    echo "[*] Cleaning up test container: $CONTAINER_NAME..."
    docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
}
trap cleanup EXIT

echo "[*] Launching ephemeral Arch Linux build container..."
docker run --name "$CONTAINER_NAME" -d \
    -v "$AUR_DIR:/aur_src:ro" \
    archlinux:base-devel sleep 3600

echo "[*] Setting up build environment and builder user in container..."
docker exec "$CONTAINER_NAME" bash -c '
set -e
echo y | pacman -Sy --noconfirm --needed go git sudo

useradd -m -G wheel builder
echo "builder ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/builder
chmod 0440 /etc/sudoers.d/builder

mkdir -p /home/builder/aur
cp -r /aur_src/* /home/builder/aur/
chown -R builder:builder /home/builder
'

echo "================================================================="
echo "[*] Building bp2ninja base package inside container..."
echo "================================================================="
docker exec -u builder -w /home/builder/aur/bp2ninja "$CONTAINER_NAME" bash -c '
set -e
makepkg -si --noconfirm
'

echo "[+] Verifying bp2ninja installation..."
docker exec "$CONTAINER_NAME" bash -c '
set -e
bp2ninja -h > /dev/null
echo "[OK] bp2ninja binary is functional: $(which bp2ninja)"
test -d /usr/share/bp2ninja/pkg
echo "[OK] bp2ninja Go module packages installed at /usr/share/bp2ninja/pkg"
test -d /usr/lib/bp2ninja/plugins
echo "[OK] System plugins directory exists: /usr/lib/bp2ninja/plugins"
'

echo "================================================================="
echo "[*] Building and Installing Plugins Out-of-Tree (Remote Mode)..."
echo "================================================================="

docker exec -u builder -w /home/builder/aur "$CONTAINER_NAME" bash -c '
set -e

PASSED=0
FAILED=0
FAILED_PKGS=()

for pkgdir in bp2ninja-plugin-*; do
    echo "--------------------------------------------------------"
    echo "[*] Building: $pkgdir"
    echo "--------------------------------------------------------"
    if (cd "$pkgdir" && makepkg -si --noconfirm); then
        echo "[OK] Successfully built and installed: $pkgdir"
        PASSED=$((PASSED + 1))
    else
        echo "[-] FAILED to build: $pkgdir"
        FAILED=$((FAILED + 1))
        FAILED_PKGS+=("$pkgdir")
    fi
done

echo "========================================================"
echo "Plugin Build Results: $PASSED PASSED, $FAILED FAILED"
echo "========================================================"

if [ $FAILED -ne 0 ]; then
    echo "Failed packages: ${FAILED_PKGS[*]}"
    exit 1
fi
'

echo "================================================================="
echo "[*] Validating End-to-End Plugin Auto-Discovery & Conversion..."
echo "================================================================="

docker exec -u builder -w /home/builder "$CONTAINER_NAME" bash -c '
set -e

echo "[*] Installed plugins in /usr/lib/bp2ninja/plugins/:"
ls -la /usr/lib/bp2ninja/plugins/

# Create a test Android.bp using various custom module types from installed plugins
mkdir -p /home/builder/e2e_test
cd /home/builder/e2e_test

cat << "EOF" > Android.bp
// AIDL plugin custom module
aidl_interface {
    name: "my_test_aidl",
    srcs: ["IMyService.aidl"],
}

// Kernel config custom module
kernel_config {
    name: "my_test_kconfig",
    srcs: ["kernel.config"],
}

// Wayland protocol custom module
wayland_protocol_codegen {
    name: "my_test_wayland",
    srcs: ["protocol.xml"],
}

// VINTF compatibility matrix custom module
vintf_compatibility_matrix {
    name: "my_test_vintf",
    srcs: ["matrix.xml"],
}

// XSDC custom module
xsd_config {
    name: "my_test_xsd",
    srcs: ["schema.xsd"],
}
EOF

touch IMyService.aidl kernel.config protocol.xml matrix.xml schema.xsd

echo "[*] Running bp2ninja without -a flags (testing system auto-discovery)..."
bp2ninja -bp Android.bp -o out.ninja

echo "[+] Inspecting generated out.ninja:"
cat out.ninja

# Verify out.ninja contains the generated edges
grep -q "my_test_aidl" out.ninja
grep -q "my_test_kconfig" out.ninja
grep -q "my_test_wayland" out.ninja
grep -q "my_test_vintf" out.ninja
grep -q "my_test_xsd" out.ninja

echo ""
echo "================================================================="
echo " [PASS] End-to-End out-of-tree validation succeeded completely!   "
echo "================================================================="
'
