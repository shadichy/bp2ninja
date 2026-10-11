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
- [Prebuilts & Script Modules](#prebuilts--script-modules)
  - [`prebuilt_etc`, `prebuilt_etc_host`](#prebuilt_etc-prebuilt_etc_host)
  - [`sh_binary`, `sh_binary_host`](#sh_binary-sh_binary_host)
  - [`python_binary`, `python_binary_host`, `python_test_host`](#python_binary-python_binary_host-python_test_host)
- [Declarative Meta-Modules](#declarative-meta-modules)
  - [`cc_defaults`, `java_defaults`](#cc_defaults-java_defaults)
  - [`filegroup`](#filegroup)
  - [`soong_config_*`](#soong_config_)

---

## C/C++ Modules

### Binaries (`cc_binary`, `cc_binary_host`)
Compiles C/C++ sources, assembles static archives, and links an executable binary placed into `out/bin/<name>`.
- `cc_binary`: Target device binary.
- `cc_binary_host`: Host workstation binary (compiled with host toolchain).

### Libraries (`cc_library`, `cc_library_shared`, `cc_library_static`, `cc_library_headers`)
- **`cc_library`**: Dual-variant target. By default, generates **both dynamic (`out/lib64/lib<name>.so`) and static (`out/lib64/lib<name>.a`)** targets in the same Ninja manifest.
- **`cc_library_shared`**: Generates only the shared dynamic library (`.so`).
- **`cc_library_static`**: Generates only the static library archive (`.a`).
- **`cc_library_headers`**: Header-only library. Emits include directories and an optional phony target. Does not compile object files.

### Tests & Benchmarks (`cc_test`, `cc_test_host`, `cc_benchmark`, `cc_fuzz`)
Compiles test binaries. These targets are omitted from the default Ninja build target to prevent tests from bloating production builds, but can be built explicitly via `ninja out/bin/<name>`.

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

---

## Prebuilts & Script Modules

### `prebuilt_etc`, `prebuilt_etc_host`
Copies configuration files, XMLs, and assets into target or host partitions (`out/etc/<name>`).

### `sh_binary`, `sh_binary_host`
Copies shell scripts and ensures executable permissions (`chmod +x`) in `out/bin/<name>`.

### `python_binary`, `python_binary_host`, `python_test_host`
Generates self-contained executable Python wrapper scripts in `out/bin/<name>` setting `PYTHONPATH`.

---

## Declarative Meta-Modules

These module types do not produce standalone build artifacts directly, but define metadata evaluated during compilation:

- **`cc_defaults`, `java_defaults`**: Template modules holding properties inherited by other modules via `defaults: ["..."]`.
- **`filegroup`**: Reusable collections of source files referenced via `:filegroup_name`.
- **`soong_config_module_type`, `soong_config_string_variable`, `soong_config_bool_variable`**: Declarative conditional configuration variables evaluated via `-config <key>=<value>`.
- **`phony`**: Declares target aliases and virtual milestones.
