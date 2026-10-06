package eval

import (
	"strings"
	"testing"

	"bp2ninja/pkg/parser"
)

func TestExtractSubdirs(t *testing.T) {
	bpContent := `
subdirs = [
    "tests",
    "tools",
]
optional_subdirs = [
    "optional",
]
build = [
    "special.bp",
]
`
	r := strings.NewReader(bpContent)
	file, errs := parser.Parse("Android.bp", r, parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}

	subdirs, buildFiles := ExtractSubdirs(file)
	if len(subdirs) != 3 {
		t.Fatalf("Expected 3 subdirs, got %d: %v", len(subdirs), subdirs)
	}
	if subdirs[0] != "tests" || subdirs[1] != "tools" || subdirs[2] != "optional" {
		t.Errorf("Unexpected subdirs list: %v", subdirs)
	}

	if len(buildFiles) != 1 || buildFiles[0] != "special.bp" {
		t.Errorf("Unexpected buildFiles: %v", buildFiles)
	}
}

func TestEvalFileInDirAndDefaults(t *testing.T) {
	parentBp := `
cc_defaults {
    name: "parent_defaults",
    cflags: ["-Wall", "-O2"],
    export_include_dirs: ["include"],
    srcs: ["common.cpp"],
}
`
	childBp := `
cc_binary {
    name: "child_tool",
    defaults: ["parent_defaults"],
    srcs: ["main.cpp"],
    local_include_dirs: ["local_inc"],
}
`
	ctx := NewContext(nil, "arm64")

	// Parse and evaluate parent in root ("")
	parentAst, errs := parser.Parse("Android.bp", strings.NewReader(parentBp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error parent: %v", errs)
	}
	if err := ctx.EvalFileInDir(parentAst, ""); err != nil {
		t.Fatalf("EvalFileInDir parent failed: %v", err)
	}

	// Verify defaults module recorded with Dir = ""
	defMod, ok := ctx.Defaults["parent_defaults"]
	if !ok {
		t.Fatalf("parent_defaults not found in ctx.Defaults")
	}
	if defMod.Dir != "" {
		t.Errorf("Expected defMod.Dir == '', got %q", defMod.Dir)
	}

	// Parse and evaluate child in subdir ("tools")
	childAst, errs := parser.Parse("tools/Android.bp", strings.NewReader(childBp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error child: %v", errs)
	}
	if err := ctx.EvalFileInDir(childAst, "tools"); err != nil {
		t.Fatalf("EvalFileInDir child failed: %v", err)
	}

	if len(ctx.Modules) != 1 {
		t.Fatalf("Expected 1 evaluated module, got %d", len(ctx.Modules))
	}

	childMod := ctx.Modules[0]
	if childMod.Name != "child_tool" {
		t.Errorf("Expected module child_tool, got %q", childMod.Name)
	}
	if childMod.Dir != "tools" {
		t.Errorf("Expected childMod.Dir == 'tools', got %q", childMod.Dir)
	}

	// Verify cflags merged
	cflags := childMod.GetStringList("cflags")
	if len(cflags) != 2 || cflags[0] != "-Wall" || cflags[1] != "-O2" {
		t.Errorf("Unexpected cflags: %v", cflags)
	}

	// Verify srcs: parent's "common.cpp" (from root) adjusted relative to child "tools" -> "../common.cpp"
	srcs := childMod.GetStringList("srcs")
	if len(srcs) != 2 {
		t.Fatalf("Expected 2 srcs, got %v", srcs)
	}
	if srcs[0] != "../common.cpp" {
		t.Errorf("Expected adjusted parent src '../common.cpp', got %q", srcs[0])
	}
	if srcs[1] != "main.cpp" {
		t.Errorf("Expected child src 'main.cpp', got %q", srcs[1])
	}

	// Verify export_include_dirs adjusted from "include" in root to "../include"
	expIncs := childMod.GetStringList("export_include_dirs")
	if len(expIncs) != 1 || expIncs[0] != "../include" {
		t.Errorf("Expected export_include_dirs ['../include'], got %v", expIncs)
	}
}

func TestApplyTargetAndroidNoHostKeys(t *testing.T) {
	bp := `
cc_library {
    name: "libtarget_test",
    target: {
        android: {
            cflags: ["-DDEVICE_FLAG"],
            srcs: ["device.c"],
        },
        bionic: {
            cflags: ["-DBIONIC_FLAG"],
        },
        not_windows: {
            cflags: ["-DHOST_NOT_WINDOWS"],
            srcs: ["host_not_win.c"],
        },
        linux: {
            cflags: ["-DHOST_LINUX"],
        },
    },
}
`
	ctxDevice := NewContext(nil, "arm64")
	ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}
	if err := ctxDevice.EvalFileInDir(ast, ""); err != nil {
		t.Fatalf("EvalFileInDir failed: %v", err)
	}

	if len(ctxDevice.Modules) != 1 {
		t.Fatalf("Expected 1 module, got %d", len(ctxDevice.Modules))
	}
	mod := ctxDevice.Modules[0]
	cflags := mod.GetStringList("cflags")
	srcs := mod.GetStringList("srcs")

	// Must contain android and bionic flags
	hasFlag := func(list []string, flag string) bool {
		for _, f := range list {
			if f == flag {
				return true
			}
		}
		return false
	}

	if !hasFlag(cflags, "-DDEVICE_FLAG") {
		t.Errorf("Expected -DDEVICE_FLAG in cflags: %v", cflags)
	}
	if !hasFlag(cflags, "-DBIONIC_FLAG") {
		t.Errorf("Expected -DBIONIC_FLAG in cflags: %v", cflags)
	}
	if hasFlag(cflags, "-DHOST_NOT_WINDOWS") {
		t.Errorf("Did NOT expect host flag -DHOST_NOT_WINDOWS in device cflags: %v", cflags)
	}
	if hasFlag(cflags, "-DHOST_LINUX") {
		t.Errorf("Did NOT expect host flag -DHOST_LINUX in device cflags: %v", cflags)
	}

	if !hasFlag(srcs, "device.c") {
		t.Errorf("Expected device.c in srcs: %v", srcs)
	}
	if hasFlag(srcs, "host_not_win.c") {
		t.Errorf("Did NOT expect host_not_win.c in device srcs: %v", srcs)
	}
}

func TestVariantMatching(t *testing.T) {
	bp := `
cc_defaults {
    name: "variant_defaults",
    static: {
        whole_static_libs: ["static_dep"],
        cflags: ["-DSTATIC_ONLY"],
    },
    shared: {
        shared_libs: ["shared_dep"],
        cflags: ["-DSHARED_ONLY"],
    },
}

cc_library_static {
    name: "libonly_static",
    defaults: ["variant_defaults"],
}

cc_library_shared {
    name: "libonly_shared",
    defaults: ["variant_defaults"],
}

cc_library {
    name: "libdual",
    defaults: ["variant_defaults"],
}
`
	ctx := NewContext(nil, "arm64")
	ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}
	if err := ctx.EvalFileInDir(ast, ""); err != nil {
		t.Fatalf("EvalFileInDir failed: %v", err)
	}

	modMap := make(map[string]*EvaluatedModule)
	for _, m := range ctx.Modules {
		modMap[m.Name] = m
	}

	// 1. cc_library_static should only have static properties
	staticMod := modMap["libonly_static"]
	if staticMod == nil {
		t.Fatalf("libonly_static not found")
	}
	if staticMod.GetStringList("whole_static_libs")[0] != "static_dep" {
		t.Errorf("Expected whole_static_libs ['static_dep'], got %v", staticMod.GetStringList("whole_static_libs"))
	}
	if len(staticMod.GetStringList("shared_libs")) != 0 {
		t.Errorf("Expected empty shared_libs on static library, got %v", staticMod.GetStringList("shared_libs"))
	}
	if staticMod.GetStringList("cflags")[0] != "-DSTATIC_ONLY" {
		t.Errorf("Expected cflags ['-DSTATIC_ONLY'], got %v", staticMod.GetStringList("cflags"))
	}

	// 2. cc_library_shared should only have shared properties
	sharedMod := modMap["libonly_shared"]
	if sharedMod == nil {
		t.Fatalf("libonly_shared not found")
	}
	if sharedMod.GetStringList("shared_libs")[0] != "shared_dep" {
		t.Errorf("Expected shared_libs ['shared_dep'], got %v", sharedMod.GetStringList("shared_libs"))
	}
	if len(sharedMod.GetStringList("whole_static_libs")) != 0 {
		t.Errorf("Expected empty whole_static_libs on shared library, got %v", sharedMod.GetStringList("whole_static_libs"))
	}
	if sharedMod.GetStringList("cflags")[0] != "-DSHARED_ONLY" {
		t.Errorf("Expected cflags ['-DSHARED_ONLY'], got %v", sharedMod.GetStringList("cflags"))
	}

	// 3. cc_library should retain sub-blocks and resolve independently via WithVariant
	dualMod := modMap["libdual"]
	if dualMod == nil {
		t.Fatalf("libdual not found")
	}
	dualStatic := dualMod.WithVariant("static")
	if len(dualStatic.GetStringList("whole_static_libs")) == 0 || dualStatic.GetStringList("whole_static_libs")[0] != "static_dep" {
		t.Errorf("Expected dualStatic to have whole_static_libs ['static_dep'], got %v", dualStatic.GetStringList("whole_static_libs"))
	}
	if len(dualStatic.GetStringList("shared_libs")) != 0 {
		t.Errorf("Expected dualStatic to have empty shared_libs, got %v", dualStatic.GetStringList("shared_libs"))
	}

	dualShared := dualMod.WithVariant("shared")
	if len(dualShared.GetStringList("shared_libs")) == 0 || dualShared.GetStringList("shared_libs")[0] != "shared_dep" {
		t.Errorf("Expected dualShared to have shared_libs ['shared_dep'], got %v", dualShared.GetStringList("shared_libs"))
	}
	if len(dualShared.GetStringList("whole_static_libs")) != 0 {
		t.Errorf("Expected dualShared to have empty whole_static_libs, got %v", dualShared.GetStringList("whole_static_libs"))
	}
}

func TestHostTargetAndFiltering(t *testing.T) {
	bp := `
cc_library_host {
    name: "libhost_only",
    srcs: ["host.c"],
    target: {
        host: {
            cflags: ["-DHOST_FLAG"],
        },
        linux_glibc: {
            cflags: ["-DGLIBC_FLAG"],
        },
    },
}

cc_library {
    name: "libhost_supported",
    host_supported: true,
    srcs: ["shared.c"],
    host: {
        cflags: ["-DTOP_HOST_FLAG"],
    },
    target: {
        linux: {
            cflags: ["-DLINUX_FLAG"],
        },
    },
}

cc_library {
    name: "libdevice_only",
    srcs: ["device_only.c"],
}
`
	ctxHost := NewContext(nil, "x86_64")
	ctxHost.IsHost = true
	ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}
	if err := ctxHost.EvalFileInDir(ast, ""); err != nil {
		t.Fatalf("EvalFileInDir failed: %v", err)
	}

	modMap := make(map[string]*EvaluatedModule)
	for _, m := range ctxHost.Modules {
		modMap[m.Name] = m
	}

	// 1. libhost_only must be enabled with host and linux_glibc flags
	hostMod := modMap["libhost_only"]
	if hostMod == nil || !hostMod.IsEnabled() {
		t.Fatalf("Expected libhost_only to be enabled")
	}
	hasFlag := func(list []string, flag string) bool {
		for _, f := range list {
			if f == flag {
				return true
			}
		}
		return false
	}
	if !hasFlag(hostMod.GetStringList("cflags"), "-DHOST_FLAG") {
		t.Errorf("Expected -DHOST_FLAG in host cflags, got: %v", hostMod.GetStringList("cflags"))
	}
	if !hasFlag(hostMod.GetStringList("cflags"), "-DGLIBC_FLAG") {
		t.Errorf("Expected -DGLIBC_FLAG in host cflags, got: %v", hostMod.GetStringList("cflags"))
	}

	// 2. libhost_supported must be enabled with top-level host and target.linux flags
	hostSuppMod := modMap["libhost_supported"]
	if hostSuppMod == nil || !hostSuppMod.IsEnabled() {
		t.Fatalf("Expected libhost_supported to be enabled")
	}
	if !hasFlag(hostSuppMod.GetStringList("cflags"), "-DTOP_HOST_FLAG") {
		t.Errorf("Expected -DTOP_HOST_FLAG in cflags, got: %v", hostSuppMod.GetStringList("cflags"))
	}
	if !hasFlag(hostSuppMod.GetStringList("cflags"), "-DLINUX_FLAG") {
		t.Errorf("Expected -DLINUX_FLAG in cflags, got: %v", hostSuppMod.GetStringList("cflags"))
	}

	// 3. libdevice_only must be disabled in host mode
	devMod := modMap["libdevice_only"]
	if devMod == nil {
		t.Fatalf("libdevice_only not found")
	}
	if devMod.IsEnabled() {
		t.Errorf("Expected libdevice_only to be disabled in host mode")
	}
}

func TestArchAndMultilibExpansion(t *testing.T) {
	bp := `
cc_library {
    name: "libarch_test",
    cflags: ["-DCOMMON"],
    arch: {
        arm64: {
            cflags: ["-DARCH_ARM64"],
            srcs: ["arm64.S"],
        },
        arm: {
            cflags: ["-DARCH_ARM"],
            srcs: ["arm32.S"],
        },
        x86_64: {
            cflags: ["-DARCH_X86_64"],
            srcs: ["x86_64.S"],
        },
        x86: {
            cflags: ["-DARCH_X86"],
            srcs: ["x86.S"],
        },
    },
    multilib: {
        lib32: {
            cflags: ["-DBITNESS_32"],
        },
        lib64: {
            cflags: ["-DBITNESS_64"],
        },
    },
    target: {
        android_arm64: {
            cflags: ["-DTARGET_ANDROID_ARM64"],
        },
        android_x86_64: {
            cflags: ["-DTARGET_ANDROID_X86_64"],
        },
    },
}
`

	testCases := []struct {
		arch          string
		expectedFlags []string
		expectedSrcs  []string
		unexpected    []string
	}{
		{
			arch:          "arm64",
			expectedFlags: []string{"-DCOMMON", "-DARCH_ARM64", "-DBITNESS_64", "-DTARGET_ANDROID_ARM64"},
			expectedSrcs:  []string{"arm64.S"},
			unexpected:    []string{"-DARCH_ARM", "-DARCH_X86", "-DARCH_X86_64", "-DBITNESS_32", "-DTARGET_ANDROID_X86_64"},
		},
		{
			arch:          "arm",
			expectedFlags: []string{"-DCOMMON", "-DARCH_ARM", "-DBITNESS_32"},
			expectedSrcs:  []string{"arm32.S"},
			unexpected:    []string{"-DARCH_ARM64", "-DARCH_X86_64", "-DBITNESS_64"},
		},
		{
			arch:          "x86_64",
			expectedFlags: []string{"-DCOMMON", "-DARCH_X86_64", "-DBITNESS_64", "-DTARGET_ANDROID_X86_64"},
			expectedSrcs:  []string{"x86_64.S"},
			unexpected:    []string{"-DARCH_ARM", "-DARCH_ARM64", "-DBITNESS_32"},
		},
		{
			arch:          "x86",
			expectedFlags: []string{"-DCOMMON", "-DARCH_X86", "-DBITNESS_32"},
			expectedSrcs:  []string{"x86.S"},
			unexpected:    []string{"-DARCH_ARM", "-DARCH_X86_64", "-DBITNESS_64"},
		},
	}

	for _, tc := range testCases {
		t.Run("Arch_"+tc.arch, func(t *testing.T) {
			ctx := NewContext(nil, tc.arch)
			ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
			if len(errs) > 0 {
				t.Fatalf("Parse error: %v", errs)
			}
			if err := ctx.EvalFileInDir(ast, ""); err != nil {
				t.Fatalf("EvalFileInDir failed: %v", err)
			}
			if len(ctx.Modules) != 1 {
				t.Fatalf("Expected 1 module, got %d", len(ctx.Modules))
			}
			mod := ctx.Modules[0]
			cflags := mod.GetStringList("cflags")
			srcs := mod.GetStringList("srcs")

			for _, exp := range tc.expectedFlags {
				found := false
				for _, f := range cflags {
					if f == exp {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("[%s] Expected flag %s in cflags %v", tc.arch, exp, cflags)
				}
			}

			for _, unexp := range tc.unexpected {
				for _, f := range cflags {
					if f == unexp {
						t.Errorf("[%s] Unexpected flag %s found in cflags %v", tc.arch, unexp, cflags)
					}
				}
			}

			for _, expSrc := range tc.expectedSrcs {
				found := false
				for _, s := range srcs {
					if s == expSrc {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("[%s] Expected src %s in srcs %v", tc.arch, expSrc, srcs)
				}
			}
		})
	}
}

func TestChainedDefaultsLocalIncludeDirs(t *testing.T) {
	bp := `
cc_defaults {
    name: "def_base",
    local_include_dirs: ["common/src/jni/main/include"],
    cflags: ["-DDEF_BASE"],
}

cc_defaults {
    name: "def_unbundled",
    defaults: ["def_base"],
    local_include_dirs: ["common/src/jni/unbundled/include"],
    cflags: ["-DDEF_UNBUNDLED"],
}

cc_library {
    name: "libchained_test",
    defaults: ["def_unbundled"],
    local_include_dirs: ["local_override/include"],
    cflags: ["-DLOCAL_OVERRIDE"],
    srcs: ["main.cc"],
}
`
	ctx := NewContext(nil, "x86_64")
	ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}
	if err := ctx.EvalFileInDir(ast, ""); err != nil {
		t.Fatalf("EvalFileInDir failed: %v", err)
	}

	if len(ctx.Modules) != 1 {
		t.Fatalf("Expected 1 module, got %d", len(ctx.Modules))
	}
	mod := ctx.Modules[0]
	incs := mod.GetStringList("local_include_dirs")
	expected := []string{
		"common/src/jni/main/include",
		"common/src/jni/unbundled/include",
		"local_override/include",
	}

	if len(incs) != len(expected) {
		t.Fatalf("Expected %d include dirs, got %d: %v", len(expected), len(incs), incs)
	}
	for i, exp := range expected {
		if incs[i] != exp {
			t.Errorf("At index %d: expected %q, got %q", i, exp, incs[i])
		}
	}

	cflags := mod.GetStringList("cflags")
	expectedFlags := []string{"-DDEF_BASE", "-DDEF_UNBUNDLED", "-DLOCAL_OVERRIDE"}
	for _, ef := range expectedFlags {
		found := false
		for _, f := range cflags {
			if f == ef {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected flag %s in cflags %v", ef, cflags)
		}
	}
}
