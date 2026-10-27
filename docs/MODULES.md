# Blueprint Module Type Reference

This document provides a comprehensive reference of the Soong and Blueprint module types recognized, evaluated, and translated by **bp2ninja**.

---

## Table of Contents

- [C/C++ Modules](#cc-modules)
  - [Binaries (`cc_binary`, `cc_binary_host`)](#binaries-cc_binary-cc_binary_host)
  - [Libraries (`cc_library`, `cc_library_shared`, `cc_library_static`, `cc_library_headers`)](#libraries-cc_library-cc_library_shared-cc_library_static-cc_library_headers)
  - [Tests & Benchmarks (`cc_test`, `cc_test_host`, `cc_benchmark`, `cc_fuzz`)](#tests--benchmarks-cc_test-cc_test_host-cc_benchmark-cc_fuzz)
  - [C/C++ Properties Matrix](#cc-properties-matrix)
- [Java & Kotlin Modules](#java--kotlin-modules)
  - [Libraries (`java_library`, `java_library_host`, `java_library_static`, `android_library`)](#libraries-java_library-java_library_host-java_library_static-android_library)
  - [Prebuilts (`java_import`, `android_library_import`)](#prebuilts-java_import-android_library_import)
  - [Executables (`java_binary`, `java_binary_host`)](#executables-java_binary-java_binary_host)
  - [Applications (`android_app`)](#applications-android_app)
  - [Java Properties Matrix](#java-properties-matrix)
- [Codegen & Feature Flag Modules](#codegen--feature-flag-modules)
  - [`genrule` & `java_genrule`](#genrule--java_genrule)
  - [`gensrcs` & `java_gensrcs`](#gensrcs--java_gensrcs)
  - [`cc_aconfig_library` & `java_aconfig_library`](#cc_aconfig_library--java_aconfig_library)
  - [`hidl_interface`](#hidl_interface)
- [Prebuilts & Script Modules](#prebuilts--script-modules)
  - [`prebuilt_etc`, `prebuilt_etc_host`](#prebuilt_etc-prebuilt_etc_host)
  - [`sh_binary`, `sh_binary_host`](#sh_binary-sh_binary_host)
  - [`python_binary`, `python_binary_host`, `python_test_host`](#python_binary-python_binary_host-python_test_host)
- [Declarative Meta-Modules](#declarative-meta-modules)
  - [`cc_defaults`, `java_defaults`](#cc_defaults-java_defaults)
  - [`filegroup`](#filegroup)
  - [`soong_config_*`](#soong_config_)
  - [`hidl_package_root`](#hidl_package_root)

---

## C/C++ Modules

### Binaries (`cc_binary`, `cc_binary_host`, `art_cc_binary`)
Compiles C/C++ sources, assembles static archives, and links an executable binary placed into `out/bin/<name>`.
- `cc_binary`, `art_cc_binary`: Target device binary.
- `cc_binary_host`: Host workstation binary (compiled with host toolchain).
- **Mass-Symlinks (`symlinks: [...]`)**: Generates discrete `symlink` build edges in `out/bin/` for every alias (e.g. `toybox` with 150+ command symlinks, `toolbox`, `init`).
- **Preferred Architecture (`symlink_preferred_arch: true`)**: Automatically creates an unadorned symlink (e.g. `out/bin/app_process -> app_process64`).

### Libraries (`cc_library`, `cc_library_shared`, `cc_library_static`, `cc_library_headers`, `art_cc_library`, `art_cc_library_static`)
- **`cc_library`, `art_cc_library`**: Dual-variant target. By default, generates **both dynamic (`out/lib64/lib<name>.so`) and static (`out/lib64/lib<name>.a`)** targets in the same Ninja manifest.
- **`cc_library_shared`**: Generates only the shared dynamic library (`.so`).
- **`cc_library_static`, `art_cc_library_static`**: Generates only the static library archive (`.a`).
  - **Composite Static Archives (MRI / `ar -M`)**: When a static library defines `whole_static_libs` (e.g. Bionic's `libc.a`, `libc_nomalloc.a`, `libc_common.a`), bp2ninja uses an MRI script (`ar_mri` rule) to combine sub-archives into a single composite `.a` static library.
- **`cc_library_headers`**: Header-only library. Emits include directories and an optional phony target. Does not compile object files.

### Startup Objects & Partial Linking (`cc_object`)
Compiles bare-metal startup objects (such as Android CRT objects `crtbegin_so`, `crtend_so`, `crtbegin_dynamic`, `crtbegin_static`, `crtend_android`, `crtbrand`):
- Compiles C and assembly files with `cc_compile` and `as_compile`.
- Combines multiple object files and referenced object dependencies (`objs: [...]`) via partial linking (`ld.lld -r` using the `partial_link` rule).
- Outputs `.o` files to `out/obj/<name>/<stem>.o` and copies to `out/lib64/<stem>.o` for linker resolution.

### BPF Programs (`bpf`)

Upstream `bpf` module type (build/soong/bpf): each source is compiled to a
BPF ELF object with `clang --target=bpf` using the canonical Bionic UAPI
include set, then stripped of BTF relocation sections (`llvm-strip`) by
default (`btf: false` disables both `-g` generation and stripping) and staged
under `out/etc/bpf/<sub_dir>/`. Supported properties: `srcs`, `cflags`,
`include_dirs`, `local_include_dirs`, `sub_dir`, `btf`, `vendor`. Objects are
registered as module outputs for APEX payload consumption (Connectivity,
tethering, netd BPF programs). Source basenames containing `_` are rejected
upstream; bp2ninja warns and continues.

### System Properties (`sysprop_library`, `.sysprop` sources)

- **`sysprop_library`**: each `.sysprop` description file is converted to C++
  (`sysprop-cli --cpp`, pinnable via `$SYSPROP_CLI`) and compiled into
  `lib<name>.so` / `lib<name>.a` with implicit runtime deps `-lbase -llog`.
  Dependents referencing the module by name in `shared_libs`/`static_libs`/
  `header_libs` automatically receive `-Iout/gen/<name>` for the generated
  headers and the matching `-l` link flag.
- **`.sysprop` sources in `cc_*` modules**: upstream Soong lets cc modules
  take `.sysprop` files in `srcs` directly; bp2ninja now generates the C++
  via the same `sysprop_cpp` rule and compiles it (order-only on the
  generated header).

### NDK ABI Libraries (`ndk_library`)
Processes NDK public ABI symbol definitions (`symbol_file: "<name>.map.txt"`):
- Generates GNU/LLVM linker version scripts using the `gen_version_script` rule in `out/gen/<name>/<name>.version_script`.
- Automatically injects `-Wl,--version-script=<path>` and dependency edges into companion `cc_library` targets.

### Dedicated Assembler Pipeline (`as_compile`)
- Assembly sources (`.s`, `.S`) are compiled through a dedicated `as_compile` rule using `asflags` and `-D__ASSEMBLY__`.
- C-specific header options (e.g. `-include freebsd-compat.h`) and type-checking flags in `cflags` are strictly excluded from assembly compilation, avoiding Clang assembler errors in Bionic.

### Tests & Benchmarks (`cc_test`, `cc_test_host`, `art_cc_test`, `art_cc_test_library`, `cc_benchmark`, `cc_fuzz`)
Compiles test binaries. These targets are omitted from the default Ninja build target to prevent tests from bloating production builds, but can be built explicitly via `ninja out/bin/<name>`.

### Bare-Metal & Self-Relocating Linkers (`linker64`)
- **`nocrt: true` / `no_default_compiler_flags: true`**: Injects `-nostartfiles` to permit custom entry points (`begin.o`).
- **`static_executable: true` / `force_static_executable: true`**: Injects `-static`.
- **`stem: "..."`**: Allows custom binary stems such as `linker64` with unadorned symlink aliases.
- **`version_script` & `linker_scripts`**: Directly passed as `-Wl,--version-script=...` and `-T...` linker options.

### ART Soong Schema & Codegen Properties
Modules in the ART compiler ecosystem and VIXL use domain-specific types and the `codegen:` property block:
- **`art_cc_*`**: Native equivalents of standard `cc_*` modules with implicit `-DART_TARGET` flags and full integration with ART defaults.
- **`codegen: { <arch>: { srcs, cflags, cppflags, static_libs, shared_libs } }`**: Declares architecture-specific code generation or simulation sources. On host builds (`--host`), expands all supported architectures (`arm`, `arm64`, `riscv64`, `x86`, `x86_64`) by default. On target builds, expands the active device architecture or the list set in `$ART_TARGET_CODEGEN_ARCHS` (e.g. for multi-arch simulation / translation on x86_64).

### C/C++ Properties Matrix

| Property | Type | Description |
| :--- | :--- | :--- |
| `srcs` | list of string | C, C++, Assembly (`.s`, `.S`, `.asm`), or Protobuf (`.proto`) source files. |
| `exclude_srcs` | list of string | Glob or file patterns excluded from compilation. |
| `cflags` | list of string | Extra compiler flags passed to both C and C++ files. |
| `cppflags` | list of string | Compiler flags passed exclusively to C++ files. |
| `conlyflags` | list of string | Compiler flags passed exclusively to C files. |
| `asflags` | list of string | Assembler flags for `.s`, `.S`, and NASM `.asm` files. |
| `include_dirs` | list of string | Include directories relative to `TopDir`. |
| `local_include_dirs` | list of string | Include directories relative to the module's directory. |
| `export_include_dirs` | list of string | Include directories exported to downstream dependers. |
| `shared_libs` | list of string | Dynamic libraries linked against (`-l<lib>`). |
| `static_libs` | list of string | Static archives linked against (`.a`). |
| `whole_static_libs` | list of string | Static archives wrapped in `-Wl,--whole-archive`. |
| `header_libs` | list of string | Header libraries whose include paths are imported. |
| `export_header_lib_headers` | list of string | Header libraries transitively exported to dependers. |
| `generated_headers` | list of string | Code generator targets providing headers (adds `-Iout/gen/<name>` and order-only deps). |
| `generated_sources` | list of string | Code generator targets providing source files. |
| `ldflags` | list of string | Linker flags passed to the link step. |
| `cpp_std` | string | C++ standard version (e.g. `c++17`, `c++20`, `gnu++20`). |
| `c_std` | string | C standard version (e.g. `c11`, `gnu11`). |
| `static_executable` | bool | Forces static linking for executable binaries. |
| `relative_install_path` | string | Subdirectory for the installed binary or library. |

---

## Java & Kotlin Modules

### Libraries (`java_library`, `java_library_host`, `java_library_static`, `android_library`)
Compiles Java and Kotlin source files into a JAR archive (`out/lib64/<name>.jar`).
- Automatically switches between pure `kotlinc`, pure `javac`, or two-stage mixed compilation.
- `javac` always runs with `-encoding UTF-8` (Android sources are UTF-8 regardless of the host locale).

### Prebuilts (`java_import`, `android_library_import`)
Packages existing prebuilt JAR archives into the output library directory.

### Executables (`java_binary`, `java_binary_host`)
Compiles a Java/Kotlin JAR archive and generates a shell wrapper script in `out/bin/<name>` invoking `java -jar <name>.jar`.

### Applications (`android_app`)
Packages an Android APK. Compiles Java/Kotlin classes into `out/lib64/<name>.jar` (exported for dependers) and produces an APK target via AAPT2.

### Java Properties Matrix

| Property | Type | Description |
| :--- | :--- | :--- |
| `srcs` | list of string | `.java`, `.kt`, or `.jar` sources (and `:current_android_jar`). |
| `libs` | list of string | Compile-time classpath dependencies (not bundled into output). |
| `static_libs` | list of string | Dependencies bundled onto the compilation classpath. |
| `jars` | list of string | Prebuilt JAR files for `java_import`. |
| `kotlincflags` | list of string | Extra flags passed to the Kotlin compiler. |
| `javacflags` | list of string | Extra flags passed to the Java compiler. |

---

## Codegen & Feature Flag Modules

### `genrule` & `java_genrule`
Executes custom shell commands to produce generated source files or assets.
- Supports variable expansions:
  - `$(in)`: Input source files.
  - `$(out)`: Output files.
  - `$(genDir)`: Target output directory (`out/gen/<name>`).
  - `$(location <label>)`: Resolves the path of a tool, tool file, or module output.
  - `$(locations <label>)`: Resolves multiple paths matching a pattern or reference.
- Automatically handles platform references such as `$(location :current_android_jar)`.

### `gensrcs` & `java_gensrcs`
Generates independent, per-source build edges for each input file using `output_extension`.
- Exports generated headers via `export_include_dirs`.
- Supports `data: [...]` runtime dependencies.

### `cc_aconfig_library` & `java_aconfig_library`
Implements Android 14+ AConfig feature flag library compilation:
- Resolves upstream `aconfig_declarations`.
- Invokes `aconfig create-cpp-lib` to generate C++ headers and source code.
- Compiles generated code into a static library and exports headers under `-Iout/gen/<name>/include`.

### `hidl_interface`
Compiles Android HIDL (Hardware Interface Definition Language) HAL declarations:
- Evaluates HIDL interface specifications (`<package>@<major>.<minor>`, e.g. `android.hidl.base@1.0`, `android.hardware.wifi@1.0`) and resolves root options (`-r<root>:<path>`) against in-tree `hidl_package_root` metadata.
- **C++ Code Generation (`_genc++`, `_genc++_headers`)**:
  - Automatically synthesizes companion `_genc++` and `_genc++_headers` modules.
  - Generates C++ source files (`<interface>All.cpp`, `types.cpp`) with `hidl-gen -L c++-sources`.
  - Generates C++ headers (`I<interface>.h`, `Bs<interface>.h`, `BnHw<interface>.h`, `BpHw<interface>.h`, `IHw<interface>.h`, `types.h`, `hwtypes.h`) with `hidl-gen -L c++-headers`.
  - Exported headers are routed under `out/gen/<name>/include`, allowing direct dependers (e.g. `libhidlbase`, HAL implementations) to link and include generated headers automatically.
- **Java Interface Deliverables (`*-java.jar`)**:
  - Automatically synthesizes Java library targets (`<package>-V<major>.<minor>-java`, e.g. `android.hidl.base-V1.0-java`).
  - Generates Java source files with `hidl-gen -L java` (or `-L java-constants`).
  - Compiles Java sources with `javac` against platform stubs (`system/android.jar`, resolving hidden APIs such as `HwParcel` and `HwBinder`) and packages into `out/lib64/<name>.jar` for platform consumption (`frameworks/base`, `services/core`).

---

## Prebuilts & Script Modules

### `prebuilt_etc`, `prebuilt_etc_host`
Copies configuration files, XMLs, and assets into target or host partitions (`out/etc/<name>`).

### `sh_binary`, `sh_binary_host`
Copies shell scripts and ensures executable permissions (`chmod +x`) in `out/bin/<name>`.

### `install_symlink`
Dedicated Android 17 / main branch module type defining declarative symlinks:
- `installed_location`: Relative output location (e.g. `bin/foo`, `lib64/libbar.so`).
- `symlink_target`: Destination path (e.g. `/system/bin/foo` or relative binary name).
- Emits a direct `rule symlink` Ninja edge (`mkdir -p $(dirname $out) && rm -f $out && ln -sf $symlink_target $out`).

### `android_filesystem`, `filesystem`
Defines partition images or filesystem trees with declarative symlinks:
- Parses `symlinks: [{ target: "...", name: "..." }]` and emits Ninja symlink edges for each declared link.

### `python_binary`, `python_binary_host`, `python_test_host`
Generates self-contained executable Python wrapper scripts in `out/bin/<name>` setting `PYTHONPATH`.

---

## Declarative Meta-Modules

These module types do not produce standalone build artifacts directly, but define metadata evaluated during compilation:

- **`cc_defaults`, `java_defaults`**: Template modules holding properties inherited by other modules via `defaults: ["..."]`. Path-valued properties (`srcs`, `local_include_dirs`, `export_include_dirs`, also inside `arch`/`target`/`multilib`) are rebased from the defaults module directory to the consumer directory; flag and library lists (`cflags`, `ldflags`, `shared_libs`, ...) are copied verbatim.
- **`filegroup`**: Reusable collections of source files referenced via `:filegroup_name`.
- **`soong_config_module_type`, `soong_config_string_variable`, `soong_config_bool_variable`**: Declarative conditional configuration variables evaluated via `-config <key>=<value>`.
- **`hidl_package_root`**: Declares a root package prefix and relative path mapping for HIDL interfaces (e.g. `name: "android.hidl", path: "system/libhidl/transport"`), used to construct `-r<package>:<path>` options for `hidl-gen`.
- **`phony`**: Declares target aliases and virtual milestones.
