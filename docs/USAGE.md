# bp2ninja User Guide & Reference

This guide provides comprehensive documentation on running and integrating **bp2ninja** into your build workflows, container environments, and CI pipelines.

---

## Table of Contents

- [Overview](#overview)
- [Command-Line Syntax](#command-line-syntax)
- [Detailed Options Reference](#detailed-options-reference)
  - [Input & Output](#input--output)
  - [Target Architecture & Cross-Compilation](#target-architecture--cross-compilation)
  - [Host Mode Execution](#host-mode-execution)
  - [Compiler & Linker Overrides](#compiler--linker-overrides)
  - [Soong Config Variables](#soong-config-variables)
  - [Modular Plugins & Workspace Tooling](#modular-plugins--workspace-tooling)
- [Environment Variables](#environment-variables)
- [Common Build Scenarios](#common-build-scenarios)
  - [1. Building Native Libraries for Android (NDK)](#1-building-native-libraries-for-android-ndk)
  - [2. Building Host Tools Outside AOSP](#2-building-host-tools-outside-aosp)
  - [3. Building Both Shared and Static Libraries](#3-building-both-shared-and-static-libraries)
  - [4. Multi-Directory Monorepo Conversion](#4-multi-directory-monorepo-conversion)
  - [5. Compiling with Protobuf (.proto) Sources](#5-compiling-with-protobuf-proto-sources)
  - [6. Compiling Android Apps & Java/Kotlin Libraries](#6-compiling-android-apps--javakotlin-libraries)
  - [7. Extractive APEX Packaging Workflows](#7-extractive-apex-packaging-workflows)
- [Subcommands](#subcommands)
  - [Querying NDKs: `-ndk-info`](#querying-ndks--ndk-info)
  - [Converting In-Tree Plugins: `convert-plugin`](#converting-in-tree-plugins-convert-plugin)
  - [Managing Go Workspaces: `gowork`](#managing-go-workspaces-gowork)
- [Troubleshooting & FAQ](#troubleshooting--faq)

---

## Overview

`bp2ninja` translates Blueprint (`Android.bp`) files directly into `build.ninja` manifests. Instead of resolving the entire 1,500-repository AOSP tree via Soong, `bp2ninja` isolates the target repository and produces build edges linking against your system's prebuilt libraries, headers, sysroots, and compilers.

---

## Command-Line Syntax

```bash
bp2ninja [OPTIONS] [path/to/Android.bp | path/to/dir]
```

If no path is specified, `bp2ninja` defaults to looking for an `Android.bp` file in the current working directory. You can provide a directory path (e.g. `bp2ninja system/core`), in which case `bp2ninja` sets its root directory there and discovers all modules within it.

---

## Detailed Options Reference

### Input & Output

- **`-bp <path>`**:
  Specifies the path to the Blueprint file or directory. If a directory is given, `bp2ninja` looks for `Android.bp` in that directory. Can also be supplied as the first positional argument.
- **`-o <path>`** *(default: `build.ninja`)*:
  Specifies where to write the generated Ninja manifest.
- **`-out <dir>`** *(default: `out`)*:
  Specifies the directory where build outputs (objects, binaries, libraries, classes, and generated sources) will be placed.
- **`-top <dir>`**:
  Top directory of the Android source tree. If omitted, `bp2ninja` falls back to the `$ANDROID_BUILD_TOP` environment variable or the current working directory (`.`).
- **`-subdirs`, `-r`** *(default: `true`)*:
  Recursively discovers and evaluates all nested `Android.bp` files found under subdirectories. This allows converting monorepos (e.g. `system/core`, `art`, `external/libchrome`) into a single consolidated `build.ninja`.
- **`-allow-missing-deps`** *(default: `true`)*:
  When an internal module dependency cannot be located in the parsed Blueprint tree, `bp2ninja` emits a phony target and bypasses the check, preventing build generation aborts.

---

### Target Architecture & Cross-Compilation

- **`-arch <arch>`** *(default: `arm64`)*:
  Target device architecture. Supported values:
  - `arm64` (aarch64)
  - `arm` (armv7-a)
  - `x86_64` (AMD64)
  - `x86` (i686)
  - `x32` (x32 ABI: x86-64 ISA with 32-bit pointers; injects `-mx32` into C, C++, assembly, and linker flags and routes outputs to `out/libx32/`. Host/glibc multilib environments only — Android has no x32 target, so this is not combinable with `-ndk`)
  - `riscv64` (RISC-V 64-bit)
- **`-api <level>`** *(default: `34`)*:
  Android API level for Bionic C library linking and platform SDK jar resolution (e.g. `30`, `32`, `34`).
- **`-ndk <path>`**:
  Path to an Android NDK installation, or the keyword `auto` to trigger automatic NDK discovery across standard paths.
- **`-ndk-version <version>`**:
  Filters auto-discovered NDKs by version tag (e.g. `latest`, `beta`, `r27`, `30`).
- **`-sysroot <dir>`**:
  Explicit path to target sysroot. If `-ndk` is provided, the sysroot is derived automatically from the NDK toolchain directory.
- **`-prebuilt-libs <dir>`**:
  Directory containing prebuilt `.so` or `.a` libraries. Passed as `-L<dir>` to linker invocations.
- **`-art-target-codegen-archs "<arch-list>"`**:
  Space-delimited list of target codegen architectures for ART compiler and VIXL modules (e.g. `"arm arm64 x86 x86_64"` for BlissOS / translation runtimes). Can also be supplied via `$ART_TARGET_CODEGEN_ARCHS`.
- **`-art-host-codegen-archs "<arch-list>"`**:
  Space-delimited list of host codegen architectures (defaults to all supported arches: `arm arm64 riscv64 x86 x86_64`). Can also be supplied via `$ART_HOST_CODEGEN_ARCHS`.

### Siso Execution (`-siso`)

- **`bp2siso` invocation alias**:
  The `make install` target installs a `bp2siso` symlink next to `bp2ninja`. Invoking the binary through it (`bp2siso <dir> -o build.ninja ...`) is equivalent to `bp2ninja -siso ...` — the flag defaults to on and can still be disabled explicitly with `-siso=false`.
- **`-siso`** *(default: `false`)*:
  Emits [Siso](https://go.chromium.org/build/siso)-specific ninja rule bindings and writes a companion `siso.star` next to the output ninja file. Siso is Google's drop-in Ninja replacement with native REAPI/RBE support. With `-siso`:
  - Deterministic rules (`protoc`, `genrule_cmd`, `ar_mri`, `gen_version_script`, `archive_static`, `symlink`, `copy`, `python_binary`) gain `restat_content = 1` — content-digest cleanliness instead of mtime, preventing rebuild cascades when generated files are re-touched with identical content.
  - Host-tool `bash -c` rules (`javac_compile`, `kotlinc_compile`, `kotlin_java_compile`, `genrule_cmd`, `ar_mri`, `gen_version_script`, `python_binary`) gain `sandbox_disabled = true` since they need host filesystem access.
  - The generated `siso.star` handles what ninja bindings cannot: `input_root_absolute_path` for compile actions (bp2ninja graphs build against sysroots outside the workspace, so depfiles legitimately contain absolute header paths) and `output_local` for link/archive steps consuming prebuilt libraries.
  - Install and run:
    ```bash
    bp2ninja -siso <dir> -o build.ninja
    mkdir -p build/config/siso && cp siso.star build/config/siso/main.star
    siso ninja -C .
    ```
  - Note: upstream `ninja` rejects these extra bindings, so a `-siso` graph must be executed by `siso` (or the Android ninja fork).
- **`-siso-remote`** *(default: `false`)*:
  With `-siso`, additionally marks pure compile actions `remote_enabled = true` for RBE offloading. Requires a REAPI backend configured on the siso side; extend the generated `siso.star` toolchain/sysroot input hooks (commented examples included) so remote actions upload the compiler context.

---

### Host Mode Execution

- **`-host`** *(default: `false`)*:
  Toggles evaluation from target device to the host build machine.
  - Expands `host: {}` property overrides, `target.host`, `target.linux`, and `target.linux_glibc`.
  - Enables `*_host` module types (`cc_binary_host`, `cc_library_host`, `java_binary_host`).
  - Disables device-only modules.
  - Uses host toolchains (`$CC`, `$CXX`, or system `clang`/`clang++`).

---

### Compiler & Linker Overrides

- **`-cc <path>`** *(default: `clang`)*:
  C compiler executable. If `-ndk` is specified, defaults to the NDK target Clang binary.
- **`-cxx <path>`** *(default: `clang++`)*:
  C++ compiler executable. If `-ndk` is specified, defaults to the NDK target Clang++ binary.
- **`-ld <path>`** *(default: `ld.lld`)*:
  Linker executable path used for partial linking (`ld.lld -r` via `partial_link` rule).
- **`-cflags <flags>`**:
  Extra C compiler flags (can also be passed via the `$CFLAGS` environment variable).
- **`-cppflags <flags>`, `-cxxflags <flags>`**:
  Extra C++ compiler flags (can also be passed via `$CXXFLAGS` or `$CPPFLAGS`).
- **`-asflags <flags>`**:
  Extra assembler flags (can also be passed via the `$ASFLAGS` environment variable).
- **`-ldflags <flags>`**:
  Extra linker flags (can also be passed via `$LDFLAGS`).
- **Plain-compiler cross-targeting (arm64 servers / Termux)**:
  In target mode, when the compiler is a plain `clang`/`gcc` (not an NDK wrapper such as `aarch64-linux-android34-clang`), bp2ninja injects `--target=<triple>` — and `--sysroot=<dir>` when `-sysroot` is set — into all compile and link flags. This makes `clang` on an arm64 headless server or a Termux device cross-target Android (`-arch arm64 -api 34`) instead of the build host ABI. NDK wrapper compilers already encode the target and receive no redundant flags.
- **`-include-root <dir>`**:
  Root directory for resolving root-relative include paths (e.g. `-Ibionic/libc/include` or `include_dirs: ["bionic/libc/include"]`) when building subprojects out of tree.

> [!NOTE]
> The generated Ninja manifest includes dynamic environment variables `$${CFLAGS}`, `$${CXXFLAGS}`, `$${ASFLAGS}`, and `$${LDFLAGS}` in its rule definitions. You can change optimization flags or linker settings at Ninja invocation time without re-running `bp2ninja`.

---

### Cross-Building bp2ninja Itself (arm64, musl, bionic)

Static builds (`CGO_ENABLED=0`) link no libc — a single binary runs on glibc, musl,
and Android hosts of the same instruction set. Go plugins (`.so` blueprint modules)
are unavailable in static builds; build natively with `make build` when plugins
are required.

```bash
make build-linux-arm64        # static arm64: headless servers, qemu, Termux
make build-android-arm64      # static GOOS=android: adb push to /data/local/tmp
make build-android-arm64-bionic ANDROID_NDK=/opt/android-ndk   # bionic-linked
make build-musl-arm64         # true musl dynamic link (needs musl-gcc)
make release                  # tarballs + bp2siso symlinks + sha256sums
```

**arm64 headless servers**: the static arm64 binary builds arm64 Android targets
natively (no emulated generation pass). Use `-ndk auto` (an x86_64-host NDK works
via qemu binfmt) or a plain `clang` plus `-sysroot <android sysroot>` — the triple
is injected automatically.

**Android self-replication (on-device builds)**: push the static android-arm64
binary to the device. On Termux, `clang` targets the device's own bionic, and
`$PREFIX`-relative NDK installations (`$PREFIX/opt/android-ndk`) are discovered
automatically. The bionic-linked variant (`build-android-arm64-bionic`) requires
matching NDK runtime libraries on the device when the API level differs.

**musl / embedded**: static builds already run on musl (Alpine) hosts; use
`make build-musl-{amd64,arm64}` when a true musl dynamic link is required
(`pacman -S musl` / `apt install musl-tools` provides `musl-gcc`).

### Soong Config Variables

- **`-config <key>=<value>`**:
  Supplies Soong config variable values for evaluating conditional blocks in `Android.bp`.
  Example:
  ```bash
  bp2ninja -config target_board_platform=sm8450 -config ufs_support=true
  ```

---

### Modular Plugins & Workspace Tooling

- **`-a <path>`, `-plugin <path>`, `-add-plugin <path>`**:
  Loads an external compiled Go plugin (`.so`) that registers custom module type handlers.
- **`-convert-plugin <dir>`**:
  Subcommand/flag to convert in-tree Soong Go rules into a dynamic `.so` plugin.
- **`-gen-gowork`**:
  Generates a Go workspace file (`go.work`) for working with Android Go source packages.
- **`-clean-gowork`**:
  Deletes `go.work` and `go.work.sum`.
- **`-tree <dir>`**:
  Specifies the Android source tree root for `go.work` generation.
- **`-remote`**:
  Allows remote cloning from `android.googlesource.com` during workspace generation.

---

## Environment Variables

| Variable | Description |
| :--- | :--- |
| `CC` | Default C compiler path when `-cc` is not explicitly set. |
| `CXX` | Default C++ compiler path when `-cxx` is not explicitly set. |
| `CFLAGS`, `CCFLAGS` | C compiler flags injected at generation time and runtime into `$cflags`. |
| `CXXFLAGS`, `CPPFLAGS` | C++ flags injected at generation time and runtime into `$cflags`. |
| `LDFLAGS` | Linker flags injected at generation time and runtime into `$ldflags`. |
| `ANDROID_BUILD_TOP` | Default root directory for `-top`. |
| `ANDROID_NDK` | Directory searched by `-ndk auto` discovery. |
| `ANDROID_SDK`, `ANDROID_HOME` | SDK root used to resolve `:current_android_jar` to `<SDK>/platforms/android-<API>/android.jar`. |
| `ART_TARGET_CODEGEN_ARCHS` | Space-delimited target codegen architectures for ART/VIXL (e.g. `"arm arm64 x86 x86_64"`). |
| `ART_HOST_CODEGEN_ARCHS` | Space-delimited host codegen architectures (defaults to all supported architectures). |
| `BP2NINJA_PLUGINS_DIR` | Directory automatically scanned for `*.so` plugins. |
| `PROTOC` | Path to Protobuf compiler executable (for ABI pinning). |
| `CLASSPATH` | Host Java classpath search path. |

---

## Common Build Scenarios

### 1. Building Native Libraries for Android (NDK)

Cross-compiling `system/core/libbase` for `x86_64` API 34 using an auto-discovered NDK:

```bash
bp2ninja -bp system/core/libbase \
         -arch x86_64 \
         -api 34 \
         -ndk auto \
         -o libbase.ninja

# Execute the compilation
ninja -f libbase.ninja
```

### 2. Building Host Tools Outside AOSP

Building host tools (such as `simg2img`, `apexer`, or `signapk`) for your workstation:

```bash
bp2ninja -bp system/core/libsparse \
         -host \
         -o sparse.ninja

ninja -f sparse.ninja out/bin/simg2img
```

### 3. Building Both Shared and Static Libraries

`bp2ninja` automatically generates both `.so` and `.a` targets for `cc_library` definitions.

```bash
bp2ninja -bp external/zlib -o zlib.ninja

# Build both simultaneously:
ninja -f zlib.ninja

# Build only the dynamic library:
ninja -f zlib.ninja out/lib64/libz.so

# Build only the static archive:
ninja -f zlib.ninja out/lib64/libz.a
```

### 4. Multi-Directory Monorepo Conversion

To convert an entire repository with multiple submodules:

```bash
bp2ninja -subdirs system/core -o system_core.ninja

ninja -f system_core.ninja
```

### 5. Compiling with Protobuf (.proto) Sources

`bp2ninja` handles `.proto` sources in subdirectories and preserves relative header output paths:

```bash
export PROTOC=/usr/bin/protoc

bp2ninja -bp system/update_engine \
         -arch x86_64 \
         -api 34 \
         -ndk auto \
         -o update_engine.ninja

ninja -f update_engine.ninja out/lib64/libupdate_metadata-protos.a
```

### 6. Compiling Android Apps & Java/Kotlin Libraries

Supports pure Kotlin, pure Java, or mixed-language compilation:

```bash
bp2ninja -bp packages/apps/ExactCalculator -o calculator.ninja

ninja -f calculator.ninja
```

### 7. Extractive APEX Packaging Workflows

In Extractive APEX recipes (`APEXBUILD`), `bp2ninja` allows standalone compilation:

```bash
build() {
  cd "$srcdir/$pkgbase"
  bp2ninja -bp Android.bp -o build.ninja -arch x86_64 -api 34 -ndk "${NDK:-auto}"
  ninja -f build.ninja
}
```

---

## Subcommands

### Querying NDKs: `-ndk-info`

Displays all discovered Android NDK installations on your system:

```bash
bp2ninja -ndk-info
```

Output includes version number, major version, Clang toolchain path, and sysroot directory.

### Converting In-Tree Plugins: `convert-plugin`

Transforms Soong Go rules into loadable `.so` plugins:

```bash
bp2ninja convert-plugin external/aidl/build -o aidl.so
```

### Managing Go Workspaces: `gowork`

Prepares an isolated `go.work` environment:

```bash
# Generate workspace
bp2ninja gowork --tree /path/to/los-21

# Remove workspace
bp2ninja clean-gowork
```

---

## Troubleshooting & FAQ

### 1. `fatal error: '<header>.pb.h' file not found`
Ensure `bp2ninja` is up to date. `bp2ninja` automatically adds `out/gen/<mod>` to include directories and attaches order-only dependencies (`|| <pbHdr>`) to object compilation edges. If a custom Protobuf compiler is required, set `export PROTOC=/path/to/protoc`.

### 2. `fatal error: 'android.jar' not found`
Ensure `$ANDROID_SDK` or `$ANDROID_HOME` is exported and contains the requested platform level:
```bash
export ANDROID_SDK=/opt/android-sdk
```

### 3. Missing dependencies warning during generation
`bp2ninja` is designed to build isolated repositories outside the full source tree. Warnings about missing internal dependencies are normal; `bp2ninja` emits fallback `-l<name>` flags and safe phony rules so Ninja can continue building.
