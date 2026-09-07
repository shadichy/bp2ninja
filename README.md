# bp2ninja

**bp2ninja** is a high-speed, standalone compiler that converts `Android.bp` (Soong/Blueprint) build manifests directly into `build.ninja` files, completely **bypassing monolithic AOSP dependency checks**.

It enables compiling Android repositories in complete isolation outside the full Android source tree, using prebuilt libraries, sysroots, and modular Go plugins.

---

## Key Features

1. **Direct BP-to-Ninja Translation:**
   Translates `cc_binary`, `cc_library`, `cc_library_shared`, `cc_library_static`, `android_app`, `java_library`, `genrule`, `prebuilt_etc`, and `sh_binary` into Ninja build rules in milliseconds.
2. **Dependency Check Bypass:**
   Instead of demanding that all dependencies (`shared_libs`, `static_libs`, `header_libs`) exist as source code in a 1,500-repo checkout, `bp2ninja` emits `-l<lib>` flags and points to prebuilt library directories (`$OUT/system/lib64`).
3. **Defaults & Variable Flattening:**
   Recursively resolves top-level variable assignments and flattens `defaults: ["..."]` (e.g., `cc_defaults`) into target modules.
4. **Declarative `soong_config` Support:**
   Evaluates conditional properties based on target SoC/board flags (e.g. `-config target_board_platform=sm8450`).
5. **Native AST Auto-Derivation (Zero Plugins Needed):**
   Automatically derives Ninja build edges for custom Soong module types (codegen, TableGen, directory collectors, packaging, build properties, Mojom, kernel configs, and VINTF compatibility matrices) directly from AST heuristics, achieving 100% pass rate across tested repositories.
6. **Zero External Go Dependencies:**
   Contains a self-contained Blueprint AST parser; builds instantly without downloading any internet packages.

---

## Directory Structure

```
bp2ninja/
├── cmd/
│   └── bp2ninja/          # Main CLI entry point
├── pkg/
│   ├── parser/            # Self-contained Blueprint AST parser
│   ├── eval/              # AST evaluator, variable resolution & defaults flattener
│   ├── ninja/             # Ninja syntax writer
│   ├── generator/         # Module translators & native auto-derivation engine
│   └── plugins/           # Dynamic .so plugin loader (optional user extensions)
├── scripts/
│   └── benchmark_coverage.py  # 150-target coverage benchmarking suite
├── Makefile
└── go.mod
```

---

## Building `bp2ninja`

Build using standard `make` or `go`:

```bash
cd tools/bp2ninja
make
```

The compiled binary will be placed at `bin/bp2ninja`.

### Go Workspace Integration (`go.work`)

For IDE support (VS Code, GoLand, `gopls`) and compiling `android/*` packages outside a monolithic checkout, generate a `go.work` workspace file:

```bash
# Auto-detect local tree or clone missing repos from googlesource.com
make gowork

# Or force remote cloning from android.googlesource.com
python3 scripts/gen_gowork.py --remote

# Clean up workspace files
make clean-gowork
```

---

## CLI Usage

```text
Usage of bp2ninja:
  -bp string
        Path to the target Android.bp file (default "Android.bp")
  -o string
        Output Ninja build file path (default "build.ninja")
  -out string
        Output directory for build artifacts (default "out")
  -top string
        Top of Android source tree (defaults to $ANDROID_BUILD_TOP or current dir)
  -sysroot string
        Path to Android sysroot / NDK (optional)
  -prebuilt-libs string
        Directory containing prebuilt .so / .a libraries
  -cc string
        C compiler executable path (default "clang")
  -cxx string
        C++ compiler executable path (default "clang++")
  -arch string
        Target architecture (arm64, arm, x86_64) (default "arm64")
  -a value
        Path to compiled Go plugin (.so) to load (repeated or comma-separated)
  -add-plugin value
        Path to compiled Go plugin (.so) to load (repeated or comma-separated)
  -plugin value
        Alias for -a/--add-plugin
  -config value
        Soong config variable in key=value format (can be repeated)
```

---

## Examples

### 1. Converting a C/C++ Repository
Convert `system/logging/logcat/Android.bp` to a local `build.ninja`:
```bash
./bin/bp2ninja \
    -bp /path/to/system/logging/logcat/Android.bp \
    -o logcat.ninja \
    -prebuilt-libs /path/to/out/target/product/generic_arm64/system/lib64
```
Then build with Ninja:
```bash
ninja -f logcat.ninja
```

### 2. Evaluating `soong_config` Board Variables
If a repository conditionally adds CFLAGS based on SoC platform:
```bash
./bin/bp2ninja \
    -bp hardware/qcom/display/Android.bp \
    -config target_board_platform=sm8450 \
    -o display.ninja
```

### 3. Converting Complex Custom Modules (Zero Plugins Needed)
For repositories containing custom Soong modules (such as Wayland protocol codegen, TableGen, ca-certificates, Cuttlefish packages, or kernel configs):

```bash
# Wayland Protocol Codegen (127 build actions generated automatically)
./bin/bp2ninja -bp external/wayland-protocols/Android.bp -o wayland.ninja

# LLVM TableGen & Clang
./bin/bp2ninja -bp external/clang/Android.bp -o clang.ninja

# Arm Compute Library (1,138 build actions generated automatically)
./bin/bp2ninja -bp external/ComputeLibrary/Android.bp -o arm.ninja
```
*Note: The `-a / --add-plugin` flag remains available if you wish to load optional external Go `.so` plugins.*

---

## Accelerated Product Build Workflow

By using `bp2ninja` to build repositories outside the main AOSP tree:
1. Repositories are compiled in parallel as standalone modules.
2. Compiled artifacts (`.apk`, `.so`, `.jar`) are reintroduced into the Android tree using Soong's native prebuilt mechanisms (`cc_prebuilt_library_shared`, `android_app_import`).
3. The main AOSP ROM build is trimmed to the **~47 core monolithic repos**, reducing full image assembly time from **~60+ minutes to ~10 minutes**.
