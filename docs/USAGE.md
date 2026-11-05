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
  - [8. Compiling HIDL Interfaces & Java Deliverables](#8-compiling-hidl-interfaces--java-deliverables)
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

### Rust & Cargo Execution (`bp2cargo`, `-cargo`)

- **`bp2cargo` invocation alias**:
  The `make install` target installs a `bp2cargo` symlink next to `bp2ninja`. Invoking the binary through it (`bp2cargo <dir> -o build.ninja ...`) is equivalent to `bp2ninja -cargo ...` — Cargo mode defaults to on.
- **`-cargo`** *(default: `false`, or `true` when invoked as `bp2cargo`)*:
  Enables Cargo manifest synthesis and drives Rust crate builds through `cargo build`.
  - For each Rust module (`rust_ffi`, `rust_library`, `rust_binary`, `rust_proc_macro`), synthesizes an idiomatic `Cargo.toml` manifest under `out/cargo/<name>/Cargo.toml`.
  - Emits `cargo_build` ninja rules targeting `x86_64-linux-android`, `aarch64-linux-android`, or host triples, injecting cross-linker settings via `CARGO_TARGET_<TRIPLE>_LINKER="$cc"`.
  - Stages compiled static archives (`.a`), shared libraries (`.so`), and binaries into `out/lib64/` and `out/bin/`, with full C-ABI linkage and header export support for dependent `cc_library` / `cc_binary` targets.
- **`-cargo-path <path>`** *(default: `cargo`, env `$CARGO`)*:
  Path to the `cargo` executable.
- **`-cargo-out-dir <dir>`**:
  Custom directory for generated `Cargo.toml` manifests (defaults to `out/cargo/<name>`).
- **`-cargo-offline`** *(default: `false`)*:
  Passes `--offline` and exports `CARGO_NET_OFFLINE=true`, ensuring airgapped builds without crates.io network access.
- **`-bindgen-path <path>`** *(default: `bindgen`, env `$BINDGEN`)*:
  Path to the `bindgen` executable used for `rust_bindgen` rules.
- **`-aidl <path>`** *(default: `aidl`, env `$AIDL`)*:
  Path to the `aidl` executable used for `aidl_interface` multi-backend codegen.
- **`-metalava <path>`** *(default: `metalava`, env `$METALAVA`)*:
  Path to the `metalava` executable used for `droidstubs` API stubs.

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
- **`-hidl-gen <path>`** *(default: `hidl-gen`)*:
  Path to `hidl-gen` executable. If omitted, `bp2ninja` checks `$HIDL_GEN`, then searches prebuilts directories (e.g. `$top/prebuilts/build-tools/linux-x86/bin/hidl-gen` or `/los-21/...`). Used for code generation on `hidl_interface` modules.
- **`-xsdc <path>`** *(default: `xsdc`)*:
  Path to `xsdc` XML schema compiler executable. Can also be supplied via `$XSDC`.
- **`-conv-linker-config <path>`** *(default: `conv_linker_config`)*:
  Path to `conv_linker_config` tool. Can also be supplied via `$CONV_LINKER_CONFIG`.
- **`-xmllint <path>`** *(default: `xmllint`)*:
  Path to `xmllint` XML validator. Can also be supplied via `$XMLLINT`.
- **`-mkbootimg <path>`** *(default: `mkbootimg`)*:
  Path to `mkbootimg` boot image creation tool. Can also be supplied via `$MKBOOTIMG`.
- **`-avbtool <path>`** *(default: `avbtool`)*:
  Path to `avbtool` Android Verified Boot utility. Can also be supplied via `$AVBTOOL`.
- **`-lpmake <path>`** *(default: `lpmake`)*:
  Path to `lpmake` dynamic partition utility. Can also be supplied via `$LPMAKE`.
- **`-compat-config <path>`** *(default: `process-compat-config`)*:
  Path to `process-compat-config` tool. Can also be supplied via `$PROCESS_COMPAT_CONFIG`.
- **`-bootclasspath <paths>`**:
  Colon-delimited bootclasspath JAR paths prepended to the head of all Java and Kotlin compilation classpaths. Can also be supplied via `$BOOTCLASSPATH`.
- **`-prebuilt-jars <paths>`**:
  Colon-delimited list of directories or files containing prebuilt JAR libraries. Discovers and prioritizes authentic physical JARs (e.g. `prebuilts/target/art/share/art/art-module-lib.jar`) ahead of synthetic fallback targets. Can also be supplied via `$PREBUILT_JARS`.
- **`-extra-jars <paths>`**:
  Colon-delimited list of extra JAR libraries or directories to inject into compilation classpaths. Can also be supplied via `$EXTRA_JARS`.
- **`-android-jar <path>`**:
  Explicit path to the Android platform SDK `android.jar`. Can also be supplied via `$ANDROID_JAR`.
- **`-java-encoding <encoding>`** *(default: `UTF-8`)*:
  Explicit source encoding for `javac` and `kotlinc` compilation. Can also be supplied via `$JAVA_ENCODING`.
- **`-skip-abi-level <n>`** *(default: `0`)*:
  Base Android ABI skip level threshold (corresponds to makeapex / apexm `--skip-abi-level`).
- **`-disable-skip-abis`** *(default: `false`)*:
  Disables base Android ABI library skipping. Forces resolution against custom in-tree/APEX platform headers and libraries (`/apex/include`, `/apex/com.android.ext.framework.base/include`), and suppresses shadowing VNDK snapshot headers.
- **`-cflags <flags>`, `-ccflags <flags>`**:
  Extra C compiler flags (can also be passed via the `$CFLAGS` or `$CCFLAGS` environment variables).
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

- **`-prefix <dir>`** *(default: relative mode)*:
  Runtime install prefix for library discovery: link edges (`link_shared` and `link_binary`) gain `-Wl,-rpath,<prefix>/<libsub>` (`lib64`, `lib`, or `libx32` by target arch) plus a deploy-relative `-Wl,-rpath,$$ORIGIN/../<libsub>` (ninja-escaped; the shell receives `$ORIGIN`), so the linked artifacts find their shared libraries — and libc — once deployed under the prefix (e.g. an extracted APEX libc under `/apex/<name>/lib64` or `/system/lib64`). Link-time library search (`-L`: out-libdir / `-prebuilt-libs` / TopDir discovery) is unchanged. Ninja always builds and outputs artifacts in the build-local `out/` layout — nothing is staged, installed, or copied into the prefix. Sysroot, NDK paths, include dirs, the TopDir dependency scan and `-prebuilt-libs` are untouched. Passing `.` or `/` (or omitting the flag) keeps the relative behavior (no rpath flags).

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
| `HIDL_GEN` | Path to `hidl-gen` executable. |
| `XSDC` | Path to `xsdc` executable. |
| `CONV_LINKER_CONFIG` | Path to `conv_linker_config` executable. |
| `XMLLINT` | Path to `xmllint` executable. |
| `MKBOOTIMG` | Path to `mkbootimg` executable. |
| `AVBTOOL` | Path to `avbtool` executable. |
| `LPMAKE` | Path to `lpmake` executable. |
| `PROCESS_COMPAT_CONFIG` | Path to `process-compat-config` executable. |
| `JAVA_ENCODING` | Default character encoding for Java compilation (default: `UTF-8`). |
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

Supports pure Kotlin, pure Java, or mixed-language compilation with a **7-tier classpath precedence hierarchy**:

```bash
bp2ninja -bp packages/apps/ExactCalculator -o calculator.ninja

ninja -f calculator.ninja
```

When building platform services or Extractive APEX components out-of-tree, physical JARs (such as `art-module-lib.jar` or `/apex/com.android.art/share/art/art-module-lib.jar`) can be injected and prioritized ahead of synthetic phony targets:

```bash
bp2ninja -bp frameworks/base/services \
         -prebuilt-jars prebuilts/target/art/share/art/art-module-lib.jar \
         -bootclasspath /apex/com.android.art/share/art/art-module-lib.jar \
         -o services.ninja

ninja -f services.ninja
```

#### 7-Tier Classpath Precedence Hierarchy:
1. **Tier 0 (Explicit Bootclasspath & Extra Injections)**: Entries from `-bootclasspath` and `-extra-jars` (or `$BOOTCLASSPATH`, `$EXTRA_JARS`), placed at the very head.
2. **Tier 1 (In-Tree / Graph-Local Module Outputs)**: Authentic outputs produced by modules in the same Ninja manifest (`g.moduleOutputs`, AIDL interfaces, Droidstubs, XSD configs, Java genrules).
3. **Tier 2 (Extractive APEX & Shadow Prebuilt JARs)**: Physical JARs located via `-prebuilt-jars`, `/apex/*/share/*/*.jar`, and `${TopDir}/prebuilts/target/*/share/*/*.jar`. Automatically maps conceptual Blueprint dependencies (`art.module.public.api.stubs.module_lib` $\to$ `art-module-lib.jar`) and **suppresses dummy phony rule generation**.
4. **Tier 3 (Platform SDK Boot & Core Jars)**: `sdk_version` boot libraries (`art-module-lib.jar`, `core-libart.jar`) and SDK `android.jar`.
5. **Tier 4 (In-Tree SDK Prebuilts)**: `${TopDir}/prebuilts/sdk/<api>/public/<lib>.jar`.
6. **Tier 5 (Host Environment `$CLASSPATH`)**: System classpath fallback.
7. **Tier 6 (Synthetic Phony Fallback)**: Generates `out/lib64/<lib>.jar` with a phony rule only if no physical JAR was found and `-allow-missing-deps` is active.

### 7. Extractive APEX Packaging Workflows

In Extractive APEX recipes (`APEXBUILD`), `bp2ninja` allows standalone compilation:

```bash
build() {
  cd "$srcdir/$pkgbase"
  bp2ninja -bp Android.bp -o build.ninja -arch x86_64 -api 34 -ndk "${NDK:-auto}"
  ninja -f build.ninja
}
```

### 8. Compiling HIDL Interfaces & Java Deliverables

Compiling HIDL interfaces and libraries (e.g. `system/libhidl`):

```bash
bp2ninja -bp system/libhidl/transport \
         -arch x86_64 \
         -api 34 \
         -hidl-gen /path/to/hidl-gen \
         -o hidl.ninja

# Build C++ libraries linked with generated sources & headers:
ninja -f hidl.ninja out/lib64/libhidlbase.so

# Build Java interface JAR deliverables:
ninja -f hidl.ninja out/lib64/android.hidl.base-V1.0-java.jar
ninja -f hidl.ninja out/lib64/android.hidl.manager-V1.0-java.jar
```

### 9. Compiling Rust C-ABI Libraries with `bp2cargo`

Synthesizes Cargo manifests and compiles Rust crates with C-ABI static archives (`.a`) and dynamic libraries (`.so`) linked into C++ modules:

```bash
# Invoke via bp2cargo alias (or bp2ninja -cargo) with offline mode
bp2cargo -bp external/rust/crates/rustc-demangle-capi \
         -arch x86_64 \
         -api 34 \
         -cargo-offline \
         -o rust.ninja

# Build C-ABI static archive
ninja -f rust.ninja out/lib64/librustc_demangle_static.a

# C/C++ modules depending on it link automatically:
# static_libs: ["librustc_demangle_static"] -> -l:librustc_demangle_static.a
```

### 10. Multi-Backend AIDL Interfaces & Metalava Stubs

Compiles AIDL interface definitions across C++, NDK, Java, and Rust backends with versioned aliases:

```bash
bp2ninja -bp hardware/interfaces/power/aidl \
         -arch x86_64 \
         -api 34 \
         -aidl aidl \
         -metalava metalava \
         -o aidl.ninja

# Build C++ and NDK shared and static libraries
ninja -f aidl.ninja out/lib64/libandroid.hardware.power-cpp.so
ninja -f aidl.ninja out/lib64/libandroid.hardware.power-ndk.so

# Build Java interface JAR
ninja -f aidl.ninja out/lib64/android.hardware.power-java.jar

# Build versioned target aliases
ninja -f aidl.ninja android.hardware.power-V1-cpp
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
bp2ninja gowork -tree /path/to/los-21

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
