# bp2ninja

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Language](https://img.shields.io/badge/Language-Go%201.21+-00ADD8.svg)](https://go.dev/)

**bp2ninja** is an ultra-fast, standalone compiler that converts `Android.bp` (Soong/Blueprint) build manifests directly into standard `build.ninja` files, **completely bypassing monolithic AOSP dependency checks**.

It enables building individual Android platform components, native libraries, HALs, Extractive APEXes, and apps in complete isolation outside the full multi-hundred-gigabyte Android source tree, utilizing prebuilt libraries, modular sysroots, Android NDKs, and modular Go plugins.

---

## Table of Contents

- [Why bp2ninja?](#why-bp2ninja)
- [Key Features](#key-features)
- [Architecture & Design](#architecture--design)
- [Installation](#installation)
- [CLI Reference](#cli-reference)
- [Environment Variables](#environment-variables)
- [Subcommands](#subcommands)
- [Common Workflows & Examples](#common-workflows--examples)
  - [1. Cross-Compiling for Target Device](#1-cross-compiling-for-target-device)
  - [2. Building for Host Architecture](#2-building-for-host-architecture)
  - [3. Dual-Variant Dynamic (.so) and Static (.a) Libraries](#3-dual-variant-dynamic-so-and-static-a-libraries)
  - [4. Protobuf (protoc) Code Generation](#4-protobuf-protoc-code-generation)
  - [5. Kotlin and Java Compilation](#5-kotlin-and-java-compilation)
  - [6. AConfig Code Generation (cc_aconfig_library)](#6-aconfig-code-generation-cc_aconfig_library)
  - [7. Recursive Multi-Module Discovery](#7-recursive-multi-module-discovery)
  - [8. Compiling Bionic Runtime Primitives](#8-compiling-bionic-runtime-primitives)
- [Documentation & Man Pages](#documentation--man-pages)
- [License](#license)

---

## Why bp2ninja?

In official AOSP builds, converting Blueprint files requires running `soong_build`, which parses every `Android.bp` file across ~1,500 repositories in the entire tree. This process:
- Takes **~25 to 30 minutes** just to resolve dependencies and produce the initial Ninja file.
- Demands gigabytes of memory and a complete checked-out tree.
- Fails immediately if even a single transitive dependency or package is absent from the workspace.

**bp2ninja** solves this by:
- Operating as an **independent, standalone Go executable** with zero external dependencies.
- Scanning and evaluating `Android.bp` trees in milliseconds (**~14 seconds for 8,000+ modules across an entire system tree**, ~1.7 ms per file).
- Bypassing unavailable source modules by automatically emitting `-l<lib>` linker flags, pointing to prebuilt library directories, and generating safe phony rules for missing targets.

---

## Key Features

- **100% Native Pure-Go Engine**: Self-contained Blueprint AST parser, evaluator, and Ninja emitter without Python dependencies or runtime CGo requirements.
- **Dual-Variant Compilation**: Automatically emits build rules for **both dynamic (`.so`) and static (`.a`)** libraries for `cc_library` modules. Objects are compiled once when flags and sources match, or cleanly partitioned (`.shared` / `.static`) when differentiated variants are declared.
- **Bionic & Low-Level Runtime Primitives**:
  - **Dedicated Assembler Pipeline**: Isolates `.s` and `.S` sources via `as_compile` rules with distinct `$asflags` (`-D__ASSEMBLY__`, `-fPIC`), eliminating C-only include errors.
  - **Android CRT Startup Objects (`cc_object`)**: Direct support for CRT objects (`crtbegin_so.o`, `crtend_so.o`, etc.) with partial linking (`ld.lld -r`) and explicit `-ld` toolchain targeting.
  - **Composite Static Archives (MRI Scripts)**: Synthesizes multi-component static archives (such as `libc.a` and `libc_nomalloc.a`) using `ar -M` scripts when `whole_static_libs` is present.
  - **Bare-Metal & Self-Relocating Linkers**: Full support for self-relocating executables (e.g. `linker64` / `ld-android.so`) configuring `stem`, `nocrt` (`-nostartfiles`), `static_executable` (`-static`), `version_script`, and `linker_scripts`.
  - **NDK Version Scripts (`ndk_library`)**: Direct generation of symbol version scripts from `.map.txt` ABI manifests via `gen_version_script`.
  - **Out-of-Tree Monorepo Root Resolution**: `-include-root` resolves root-relative include paths (e.g. `bionic/libc`, `system/core/...`) cleanly in standalone subprojects.
- **arm64 & Embedded Deployment (musl / bionic)**: `make build-linux-arm64` produces a fully static arm64 binary (runs natively on arm64 headless servers and under qemu); `make build-android-arm64` produces an on-device Android build (`adb push` to `/data/local/tmp`, pairs with Termux clang); `make build-android-arm64-bionic` links against bionic via the NDK toolchain; `make build-musl-{amd64,arm64}` builds true musl-linked binaries via `musl-gcc` (static builds already run on musl hosts). Plain compilers in target mode automatically receive `--target=<triple>` / `--sysroot=<dir>` so arm64 servers cross-target Android correctly without NDK wrappers.
- **Android NDK Auto-Discovery**: Automatically locates installed Android NDKs across Google Android Studio (`~/Android/Sdk/ndk/*`), Linux distribution packages (`/opt/android-ndk`, `/opt/android-ndk-beta`), and environment paths (`$ANDROID_NDK`).
- **Target & Host Modes**:
  - Target: Cross-compiles for `arm64`, `arm`, `x86_64`, `x86`, `x32`, or `riscv64` against specified Android API levels (`-api 34`). `x32` selects the x32 ABI (`-mx32`, host/glibc multilib environments).
  - Host: `-host` mode switches to host compilers (`$CC`, `$CXX`), expanding `host: {}` blocks, `target.host`, `target.linux`, and `target.linux_glibc`.
- **Runtime Environment Overrides**: Generated Ninja rules incorporate `$${CFLAGS}`, `$${CCFLAGS}`, `$${CXXFLAGS}`, `$${CPPFLAGS}`, `$${ASFLAGS}`, and `$${LDFLAGS}` variables, allowing callers to override compilation flags at `ninja` run time without re-running `bp2ninja`.
- **Protobuf (`protoc`) Generator**: Automatically transforms `.proto` files into C++ headers and source files, preserves subpath directory layouts (e.g. `protos/aconfig_storage_metadata.pb.h`), adds output include paths, and links order-only header dependencies (`|| <pbHdr>`). Supports ABI pinning via `$PROTOC`.
- **AConfig (`cc_aconfig_library`)**: Supports Android 14+ feature flag declarations, invoking `aconfig create-cpp-lib` to produce headers and static archives.
- **Kotlin & Java Pipeline**: Full support for `java_library`, `java_binary`, `java_import`, and `android_app`. Orchestrates two-stage compilation: `kotlinc` compiles Kotlin bytecode followed by `javac` against Kotlin outputs.
- **Platform `android.jar` Resolution**: Dynamic resolution of `:current_android_jar` and `:system_android_jar` to `<SDK>/platforms/android-<API>/android.jar` matching target API level.
- **Siso Backend Support (`-siso` / `bp2siso` alias)**: A `bp2siso` symlink to the binary turns `-siso` on by default (`bp2siso <dir> -o build.ninja` ≡ `bp2ninja -siso ...`). Emits Siso-compatible ninja bindings (`restat_content` on deterministic codegen rules, `sandbox_disabled` on host-tool rules) plus a generated `siso.star` Starlark config, so the graph executes under Google's [Siso](https://go.chromium.org/build/siso) (`siso ninja`) as a drop-in Ninja replacement with native REAPI/RBE offloading (`-siso-remote` marks compile actions `remote_enabled`).
- **Granular Codegen (`genrule`, `gensrcs`)**: Per-source edge generation for `gensrcs`, `export_include_dirs`, consumer wiring (`generated_sources`, `generated_headers`), and `data: [...]` dependency tracking.
- **Recursive Subdirectory Discovery**: Discovers nested `Android.bp` trees automatically via `-subdirs` (`-r`), unifying multi-module checkouts into a single cohesive Ninja graph.
- **Pure-Go Modular Plugins**: In-tree Go converter (`convert-plugin`) and workspace orchestrator (`gowork`) to convert custom Soong rules into dynamically loaded `.so` plugins, with auto-discovery in `/usr/lib/bp2ninja/plugins/`.

---

## Architecture & Design

```
bp2ninja/
├── cmd/
│   └── bp2ninja/          # Main CLI entry point and subcommand dispatcher
├── pkg/
│   ├── parser/            # Blueprint lexer, AST parser, and pretty-printer
│   ├── eval/              # AST evaluator, variable resolution, and defaults flattener
│   ├── generator/         # Ninja rule generators (cc, java, genrule, aconfig, discovery)
│   ├── ndk/               # Android NDK discovery and toolchain resolution
│   ├── ninja/             # Low-level Ninja syntax writer
│   ├── plugins/           # Dynamic .so plugin loader and Go converter
│   └── gowork/            # Go workspace (go.work) generator for Android modules
├── docs/                  # Detailed documentation (Architecture, Usage, Modules)
├── man/                   # UNIX man1 manual page (bp2ninja.1)
├── Makefile               # Build recipes
└── go.mod                 # Go module definition
```

---

## Installation

### From Source

Ensure Go 1.21 or newer is installed:

```bash
git clone https://github.com/shadichy/bp2ninja.git
cd bp2ninja
make build
```

The compiled binary will be placed at `bin/bp2ninja`. You can copy or link it to `~/.local/bin` or `/usr/local/bin`:

```bash
install -Dm755 bin/bp2ninja ~/.local/bin/bp2ninja
```

### Cross-Builds: arm64 Servers, musl & bionic (Android On-Device)

Static builds (`CGO_ENABLED=0`) link no libc at all — one binary runs on glibc,
musl, and Android hosts of the same instruction set. Go plugins (`.so` blueprint
modules) are unavailable in static builds; build natively with `make build`
(CGO enabled) when plugins are needed.

| Target | Command | Artifact |
| :--- | :-- | :-- |
| linux amd64 (static, any libc) | `make build-linux-amd64` | `bin/release/bp2ninja-linux-amd64` |
| linux arm64 (static, headless servers / Termux) | `make build-linux-arm64` | `bin/release/bp2ninja-linux-arm64` |
| Android arm64 (static, on-device) | `make build-android-arm64` | `bin/release/bp2ninja-android-arm64` |
| Android arm64 (bionic-linked via NDK) | `make build-android-arm64-bionic ANDROID_NDK=<ndk>` | `bin/release/bp2ninja-android-arm64-bionic` |
| musl amd64/arm64 (dynamic, via `musl-gcc`) | `make build-musl-arm64` | `bin/release/bp2ninja-musl-arm64` |
| Release tarballs + `bp2siso` symlinks + checksums | `make release` | `bin/release/*.tar.gz` |

On-device usage (Termux or `adb push` to `/data/local/tmp`):

```bash
make build-android-arm64
adb push bin/release/bp2ninja-android-arm64 /data/local/tmp/bp2ninja
adb shell '/data/local/tmp/bp2ninja -arch arm64 -host /path/to/module -o build.ninja'
```

Termux's `clang` already targets the device's bionic, so plain target/host
builds work without an NDK. On arm64 Linux servers, bp2ninja automatically
injects `--target=<triple>` (plus `--sysroot=<dir>` when `-sysroot` is given)
into compile/link flags whenever the compiler is a plain `clang` — NDK wrapper
compilers are detected and left untouched. NDK toolchains that only ship
`linux-x86_64` host prebuilts are still discovered on arm64 hosts (usable with
qemu-user binfmt).

### Arch Linux / CachyOS (AUR)

```bash
yay -S bp2ninja
```

---

## CLI Reference

```text
Usage: bp2ninja [options] [path/to/Android.bp | path/to/dir]
```

### Core Options

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `-bp` | string | `Android.bp` | Path to target `Android.bp` file or directory (or positional arg). |
| `-o` | string | `build.ninja` | Output Ninja build manifest path. |
| `-out` | string | `out` | Directory for build outputs and generated intermediates. |
| `-top` | string | `.` | Android source tree root (defaults to `$ANDROID_BUILD_TOP` or current dir). |
| `-include-root` | string | `""` | Root directory used to resolve root-relative includes out-of-tree (defaults to `-top`). |
| `-host` | bool | `false` | Build for host environment instead of target (expands `host: {}` properties). |
| `-subdirs`, `-r` | bool | `true` | Recursively discover nested `Android.bp` files across subdirectories. |
| `-allow-missing-deps` | bool | `true` | Generate phony targets for missing inputs/dependencies to bypass check failures. |

### Architecture & Toolchain Options

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `-arch` | string | `arm64` | Target architecture: `arm64`, `arm`, `x86_64`, `x86`, `x32` (x32 ABI, `-mx32`, host/glibc only), `riscv64`. |
| `-siso` | bool | `false` (`true` when invoked as `bp2siso`) | Emit Siso-specific ninja bindings (`restat_content`, `sandbox_disabled`) plus a companion `siso.star` config so the graph runs under `siso ninja`. |
| `-siso-remote` | bool | `false` | With `-siso`: additionally mark pure compile actions (`cc_compile`, `cxx_compile`, `as_compile`) `remote_enabled` for RBE offloading. |
| `-api` | int | `34` | Target Android API level for NDK compilation. |
| `-ndk` | string | `""` | Path to Android NDK, or `'auto'` for automatic discovery. |
| `-ndk-version` | string | `""` | Preferred NDK version (e.g. `'latest'`, `'beta'`, `'r27'`, `'30'`). |
| `-ndk-info` | bool | `false` | Query and list all discovered Android NDK installations and exit. |
| `-sysroot` | string | `""` | Path to Android sysroot (auto-derived if `-ndk` is specified). |
| `-prebuilt-libs` | string | `""` | Directory containing prebuilt libraries (`.so` / `.a`) for linker search path. |
| `-cc` | string | `clang` | C compiler executable path (or via `$CC`). |
| `-cxx` | string | `clang++` | C++ compiler executable path (or via `$CXX`). |
| `-ld` | string | `ld.lld` | Linker executable path (or via `$LD`), used for `cc_object` partial linking (`ld.lld -r`). |

### Flag & Macro Overrides

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `-cflags` | string | `""` | Extra C compiler flags (can also be passed via `$CFLAGS`). |
| `-cppflags`, `-cxxflags` | string | `""` | Extra C++ compiler flags (can also be passed via `$CXXFLAGS` / `$CPPFLAGS`). |
| `-asflags` | string | `""` | Extra assembler flags for `.s`/`.S` files (can also be passed via `$ASFLAGS`). |
| `-ldflags` | string | `""` | Extra linker flags (can also be passed via `$LDFLAGS`). |
| `-config` | repeated | `[]` | Soong config variable in `key=value` format (e.g. `-config board=sm8450`). |

### Plugin & Go Workspace Options

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `-a`, `-plugin`, `-add-plugin`| repeated | `[]` | Path to compiled Go plugin (`.so`) to load. |
| `-convert-plugin` | string | `""` | Convert in-tree Soong plugin sources into a `bp2ninja` `.so` plugin. |
| `-bp2ninja-dir` | string | `""` | Path to `bp2ninja` root source directory (used with `-convert-plugin`). |
| `-gen-gowork` | bool | `false` | Generate `go.work` workspace resolving Android platform Go packages. |
| `-clean-gowork` | bool | `false` | Remove `go.work` and `go.work.sum`. |
| `-tree` | string | `""` | Android source tree root for `go.work` generation. |
| `-remote` | bool | `false` | Clone missing packages remotely from GoogleSource during `go.work` generation. |

---

## Environment Variables

| Variable | Description |
| :--- | :--- |
| `CC` | Default C compiler path when `-cc` is not passed. |
| `CXX` | Default C++ compiler path when `-cxx` is not passed. |
| `LD` | Default linker path when `-ld` is not passed (used for `ld.lld -r` partial linking). |
| `CFLAGS` / `CCFLAGS` | Compiler flags injected at generation time and runtime into `$cflags`. |
| `CXXFLAGS` / `CPPFLAGS` | C++ flags injected at generation time and runtime into `$cflags`. |
| `ASFLAGS` | Assembler flags injected at generation time and runtime into `$asflags`. |
| `LDFLAGS` | Linker flags injected at generation time and runtime into `$ldflags`. |
| `ANDROID_BUILD_TOP` | Fallback path for `-top`. |
| `ANDROID_NDK` | Path to Android NDK installation (checked by auto-discovery). |
| `ANDROID_SDK` / `ANDROID_HOME` | Path to Android SDK (used to locate `<SDK>/platforms/android-<API>/android.jar`). |
| `BP2NINJA_PLUGINS_DIR` | Directory containing dynamic `.so` plugins for auto-loading. |
| `PROTOC` | Custom path to Protobuf compiler (useful for ABI pinning to Protobuf 21.12). |
| `CLASSPATH` | Host Java classpath search path. |

---

## Subcommands

In addition to standard flags, `bp2ninja` provides first-class CLI subcommands:

```bash
# Display discovered Android NDK installations
bp2ninja -ndk-info

# Compile an in-tree Soong plugin package into a bp2ninja .so plugin
bp2ninja convert-plugin path/to/soong/rules -o rules.so

# Generate a go.work workspace for IDE and plugin compilation
bp2ninja gowork --tree /path/to/android/tree [--remote]

# Clean up go.work artifacts
bp2ninja clean-gowork
```

---

## Common Workflows & Examples

### 1. Cross-Compiling for Target Device

Convert an Android repository (e.g. `system/core/libbase`) targeting `x86_64` API 34 using an auto-discovered NDK:

```bash
bp2ninja -bp system/core/libbase \
         -arch x86_64 \
         -api 34 \
         -ndk auto \
         -prebuilt-libs /path/to/sysroot/lib64 \
         -o libbase.ninja

# Build with Ninja
ninja -f libbase.ninja
```

### 2. Building for Host Architecture

Build host tools (e.g. `apexer`, `simg2img`, `signapk`) using host Clang and host libraries:

```bash
bp2ninja -bp system/apex/apexer \
         -host \
         -o apexer.ninja

ninja -f apexer.ninja
```

### 3. Dual-Variant Dynamic (.so) and Static (.a) Libraries

For any `cc_library`, `bp2ninja` outputs build edges for both formats:

```bash
bp2ninja -bp external/zlib -o zlib.ninja

# Build BOTH dynamic and static libraries:
ninja -f zlib.ninja

# Build ONLY the shared library:
ninja -f zlib.ninja out/lib64/libz.so

# Build ONLY the static library archive:
ninja -f zlib.ninja out/lib64/libz.a
```

### 4. Protobuf (protoc) Code Generation

For modules defining `.proto` files inside subdirectories:

```bash
# Pin ABI-matched protoc if needed
export PROTOC=/usr/bin/protoc

bp2ninja -bp system/update_engine -arch x86_64 -api 34 -ndk auto -o update_engine.ninja
ninja -f update_engine.ninja out/lib64/libupdate_metadata-protos.a
```

### 5. Kotlin and Java Compilation

For Android apps or libraries with mixed Kotlin + Java sources:

```bash
bp2ninja -bp packages/apps/ExactCalculator -o calculator.ninja
ninja -f calculator.ninja
```

### 6. AConfig Code Generation (`cc_aconfig_library`)

When building libraries depending on Android 14+ feature flags:

```bash
bp2ninja -bp frameworks/base/libs/androidfw -o androidfw.ninja
ninja -f androidfw.ninja
```

### 7. Recursive Multi-Module Discovery

Pass a directory containing many subprojects to generate a single consolidated Ninja manifest:

```bash
bp2ninja system/core -o system_core.ninja
ninja -f system_core.ninja
```

### 8. Compiling Bionic Runtime Primitives

When compiling Bionic components out-of-tree, use `-include-root` to resolve root-relative includes and build CRT startup objects, composite static archives, and bare-metal linkers:

```bash
bp2ninja -bp bionic/libc \
         -include-root bionic \
         -arch arm64 \
         -api 34 \
         -ndk auto \
         -o libc.ninja

# Build composite libc.a (via MRI script) and CRT object (via ld.lld -r):
ninja -f libc.ninja out/lib64/libc.a out/lib64/crtbegin_so.o
```

---

## Documentation & Man Pages

Comprehensive documentation is available in the [`docs/`](docs/) directory and UNIX man page:

- [**docs/USAGE.md**](docs/USAGE.md) — Comprehensive user guide, CLI reference, and troubleshooting.
- [**docs/ARCHITECTURE.md**](docs/ARCHITECTURE.md) — Internal architecture, AST pipeline, evaluator, and rule generation.
- [**docs/MODULES.md**](docs/MODULES.md) — Supported Blueprint module types, properties, and variant matrices.
- [**man/bp2ninja.1**](man/bp2ninja.1) — Standard UNIX manual page. View via `man -l man/bp2ninja.1`.

To install the man page on your system:

```bash
install -Dm644 man/bp2ninja.1 /usr/share/man/man1/bp2ninja.1
```

---

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
