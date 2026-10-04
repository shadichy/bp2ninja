package generator

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

func TestGenerateWithSubdirectories(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.BpDir = "my_project"
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	// Parent module in root
	parentMod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libparent",
		Dir:  "",
		Properties: map[string]interface{}{
			"name":                "libparent",
			"srcs":                []interface{}{"parent.cpp"},
			"export_include_dirs": []interface{}{"include"},
		},
	}

	// Child module in tests/
	childMod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "child_test",
		Dir:  "tests",
		Properties: map[string]interface{}{
			"name":        "child_test",
			"srcs":        []interface{}{"test.cpp"},
			"static_libs": []interface{}{"libparent"},
		},
	}

	modules := []*eval.EvaluatedModule{parentMod, childMod}
	if err := gen.Generate(modules); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// Check parent source compilation
	if !strings.Contains(ninjaContent, "parent.cpp") {
		t.Errorf("Expected parent.cpp in ninja output")
	}

	// Check child source compilation has tests/ prefix
	if !strings.Contains(ninjaContent, "tests/test.cpp") {
		t.Errorf("Expected tests/test.cpp in ninja output")
	}

	// Check child test references libparent's exported include dir
	if !strings.Contains(ninjaContent, "-Iinclude") {
		t.Errorf("Expected -Iinclude inherited from libparent")
	}

	// Check child test links against libparent.a
	if !strings.Contains(ninjaContent, "libparent.a") {
		t.Errorf("Expected child_test to link against libparent.a")
	}
}

func TestDedupCompoundFlags(t *testing.T) {
	input := []string{
		"-fPIC",
		"-idirafter", "/path1",
		"-isystem", "/syspath",
		"-idirafter", "/path2",
		"-fPIC",
		"-idirafter", "/path1",
		"-isystem", "/syspath",
	}

	result := dedup(input)

	// Expected:
	// -fPIC
	// -idirafter /path1
	// -isystem /syspath
	// -idirafter /path2
	expected := []string{
		"-fPIC",
		"-idirafter", "/path1",
		"-isystem", "/syspath",
		"-idirafter", "/path2",
	}

	if len(result) != len(expected) {
		t.Fatalf("Expected %d items, got %d: %v", len(expected), len(result), result)
	}

	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("At index %d: expected %q, got %q (full result: %v)", i, expected[i], result[i], result)
		}
	}
}

func TestLinkImplicitsAndDualVariant(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	// Helper dependency libraries
	wholeMod := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "libwhole",
		Dir:  "",
		Properties: map[string]interface{}{
			"name": "libwhole",
			"srcs": []interface{}{"whole.cpp"},
		},
	}
	sharedDepMod := &eval.EvaluatedModule{
		Type: "cc_library_shared",
		Name: "libshared_dep",
		Dir:  "",
		Properties: map[string]interface{}{
			"name": "libshared_dep",
			"srcs": []interface{}{"shared_dep.cpp"},
		},
	}

	// Dual-variant library (like libselinux)
	dualMod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libdual",
		Dir:  "",
		Properties: map[string]interface{}{
			"name": "libdual",
			"srcs": []interface{}{"dual.cpp"},
			"static": map[string]interface{}{
				"whole_static_libs": []interface{}{"libwhole"},
			},
			"shared": map[string]interface{}{
				"shared_libs": []interface{}{"libshared_dep"},
			},
		},
	}

	// Binary linking against libdual dynamically
	binMod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "my_app",
		Dir:  "",
		Properties: map[string]interface{}{
			"name":        "my_app",
			"srcs":        []interface{}{"app.cpp"},
			"shared_libs": []interface{}{"libdual"},
		},
	}

	modules := []*eval.EvaluatedModule{wholeMod, sharedDepMod, dualMod, binMod}
	if err := gen.Generate(modules); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// 1. libdual.so build edge should link against libshared_dep (-lshared_dep)
	if !strings.Contains(ninjaContent, "-lshared_dep") {
		t.Errorf("Expected libdual.so to link -lshared_dep")
	}

	libDir := gen.libDir()
	libdualSo := filepath.Join(libDir, "libdual.so")
	libdualA := filepath.Join(libDir, "libdual.a")
	libsharedDepSo := filepath.Join(libDir, "libshared_dep.so")
	libwholeA := filepath.Join(libDir, "libwhole.a")

	// 2. libdual.so must NOT contain --whole-archive -lwhole
	lines := strings.Split(ninjaContent, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "build "+libdualSo+":") {
			if strings.Contains(line, "libwhole.a") {
				t.Errorf("libdual.so should not have libwhole.a as implicit: %s", line)
			}
			if !strings.Contains(line, libsharedDepSo) {
				t.Errorf("libdual.so should have %s as implicit: %s", libsharedDepSo, line)
			}
		}
		if strings.HasPrefix(line, "build "+libdualA+":") {
			if !strings.Contains(line, libwholeA) {
				t.Errorf("libdual.a should have %s as implicit: %s", libwholeA, line)
			}
			if strings.Contains(line, "libshared_dep") {
				t.Errorf("libdual.a should not have libshared_dep as implicit: %s", line)
			}
		}
		if strings.HasPrefix(line, "build out/bin/my_app:") {
			if !strings.Contains(line, libdualSo) {
				t.Errorf("my_app should have %s as implicit dependency: %s", libdualSo, line)
			}
			if strings.Contains(line, "libdual.a") {
				t.Errorf("my_app should NOT have libdual.a as implicit dependency: %s", line)
			}
		}
	}
}

func TestCppStdAndCStd(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libstd_test",
		Properties: map[string]interface{}{
			"name":    "libstd_test",
			"cpp_std": "gnu++20",
			"c_std":   "gnu11",
			"srcs":    []interface{}{"foo.cpp", "bar.c"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	if !strings.Contains(ninjaContent, "-std=gnu++20") {
		t.Errorf("Expected -std=gnu++20 in cppflags for foo.cpp, got:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "-std=gnu11") {
		t.Errorf("Expected -std=gnu11 in conlyflags for bar.c, got:\n%s", ninjaContent)
	}
}

func TestModuleRootIncludeDir(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libprocessgroup_test",
		Dir:  "system/core/libprocessgroup",
		Properties: map[string]interface{}{
			"name": "libprocessgroup_test",
			"srcs": []interface{}{"cgroup_map.cpp"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	if !strings.Contains(ninjaContent, "-Isystem/core/libprocessgroup") {
		t.Errorf("Expected -Isystem/core/libprocessgroup in includes, got:\n%s", ninjaContent)
	}
}

func TestDifferentiatedVariantSources(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	// Simulating bionic libc where static and shared define separate sources and flags
	mod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libc_test",
		Properties: map[string]interface{}{
			"name": "libc_test",
			"static": map[string]interface{}{
				"srcs":   []interface{}{"static_only.c"},
				"cflags": []interface{}{"-DLIBC_STATIC"},
			},
			"shared": map[string]interface{}{
				"srcs":   []interface{}{"shared_only.c"},
				"cflags": []interface{}{"-DLIBC_SHARED"},
			},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// Static compile rule: out/obj/libc_test.static/static_only.o with -DLIBC_STATIC
	if !strings.Contains(ninjaContent, "out/obj/libc_test.static/static_only.o") {
		t.Errorf("Expected out/obj/libc_test.static/static_only.o in ninja output")
	}
	if !strings.Contains(ninjaContent, "-DLIBC_STATIC") {
		t.Errorf("Expected -DLIBC_STATIC in static compile flags")
	}

	// Shared compile rule: out/obj/libc_test.shared/shared_only.o with -DLIBC_SHARED
	if !strings.Contains(ninjaContent, "out/obj/libc_test.shared/shared_only.o") {
		t.Errorf("Expected out/obj/libc_test.shared/shared_only.o in ninja output")
	}
	if !strings.Contains(ninjaContent, "-DLIBC_SHARED") {
		t.Errorf("Expected -DLIBC_SHARED in shared compile flags")
	}

	libDir := gen.libDir()
	lines := strings.Split(ninjaContent, "\n")
	for _, line := range lines {
		// libc_test.a should only contain static_only.o
		if strings.HasPrefix(line, "build "+filepath.Join(libDir, "libc_test.a")+":") {
			if !strings.Contains(line, "static_only.o") {
				t.Errorf("libc_test.a should archive static_only.o: %s", line)
			}
			if strings.Contains(line, "shared_only.o") {
				t.Errorf("libc_test.a should NOT archive shared_only.o: %s", line)
			}
		}
		// libc_test.so should only contain shared_only.o
		if strings.HasPrefix(line, "build "+filepath.Join(libDir, "libc_test.so")+":") {
			if !strings.Contains(line, "shared_only.o") {
				t.Errorf("libc_test.so should link shared_only.o: %s", line)
			}
			if strings.Contains(line, "static_only.o") {
				t.Errorf("libc_test.so should NOT link static_only.o: %s", line)
			}
		}
	}
}

func TestDiscoverBpFilesWithoutRootBp(t *testing.T) {
	tempDir := t.TempDir()

	// Create subdirectories with Android.bp, but no Android.bp at root
	subA := filepath.Join(tempDir, "subA")
	subB := filepath.Join(tempDir, "subB")
	subC := filepath.Join(tempDir, "subA", "subC")
	ignoredDir := filepath.Join(tempDir, ".hidden")
	outDir := filepath.Join(tempDir, "out")

	for _, d := range []string{subA, subB, subC, ignoredDir, outDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("Failed to create dir %s: %v", d, err)
		}
		if err := os.WriteFile(filepath.Join(d, "Android.bp"), []byte("// test"), 0644); err != nil {
			t.Fatalf("Failed to write bp file in %s: %v", d, err)
		}
	}

	discovered, err := DiscoverBpFiles(tempDir)
	if err != nil {
		t.Fatalf("DiscoverBpFiles failed: %v", err)
	}

	if len(discovered) != 3 {
		t.Fatalf("Expected 3 discovered files, got %d: %+v", len(discovered), discovered)
	}

	expected := []struct {
		relDir string
		depth  int
	}{
		{"subA", 1},
		{"subB", 1},
		{"subA/subC", 2},
	}

	for i, exp := range expected {
		if discovered[i].RelDir != filepath.FromSlash(exp.relDir) {
			t.Errorf("At index %d: expected RelDir %q, got %q", i, exp.relDir, discovered[i].RelDir)
		}
		if discovered[i].Depth != exp.depth {
			t.Errorf("At index %d: expected Depth %d, got %d", i, exp.depth, discovered[i].Depth)
		}
	}
}

func TestDependencyOrderedModuleEmission(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	// Declare binary first, library second, leaf static library third
	appMod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "my_app",
		Properties: map[string]interface{}{
			"name":        "my_app",
			"srcs":        []interface{}{"main.cpp"},
			"shared_libs": []interface{}{"libmid"},
		},
	}
	midMod := &eval.EvaluatedModule{
		Type: "cc_library_shared",
		Name: "libmid",
		Properties: map[string]interface{}{
			"name":        "libmid",
			"srcs":        []interface{}{"mid.cpp"},
			"static_libs": []interface{}{"libleaf"},
		},
	}
	leafMod := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "libleaf",
		Properties: map[string]interface{}{
			"name": "libleaf",
			"srcs": []interface{}{"leaf.cpp"},
		},
	}

	modules := []*eval.EvaluatedModule{appMod, midMod, leafMod}
	if err := gen.Generate(modules); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// Check relative positions of build edges in ninja file
	leafIdx := strings.Index(ninjaContent, "build out/lib64/libleaf.a: archive_static")
	if leafIdx == -1 {
		leafIdx = strings.Index(ninjaContent, "libleaf.a")
	}
	midIdx := strings.Index(ninjaContent, "build out/lib64/libmid.so: link_shared")
	if midIdx == -1 {
		midIdx = strings.Index(ninjaContent, "libmid.so")
	}
	appIdx := strings.Index(ninjaContent, "build out/bin/my_app: link_binary")
	if appIdx == -1 {
		appIdx = strings.Index(ninjaContent, "my_app")
	}

	if leafIdx == -1 || midIdx == -1 || appIdx == -1 {
		t.Fatalf("Missing targets in ninja content: leaf=%d, mid=%d, app=%d", leafIdx, midIdx, appIdx)
	}

	if !(leafIdx < midIdx && midIdx < appIdx) {
		t.Errorf("Expected emission order libleaf < libmid < my_app, but got indices: leaf=%d, mid=%d, app=%d",
			leafIdx, midIdx, appIdx)
	}
}

func TestCircularDependencyFallback(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	// Mutual dependency between cycleA and cycleB; user_app depends on cycleA
	cycleA := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "cycleA",
		Properties: map[string]interface{}{
			"name":        "cycleA",
			"srcs":        []interface{}{"a.cpp"},
			"static_libs": []interface{}{"cycleB"},
		},
	}
	cycleB := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "cycleB",
		Properties: map[string]interface{}{
			"name":        "cycleB",
			"srcs":        []interface{}{"b.cpp"},
			"static_libs": []interface{}{"cycleA"},
		},
	}
	userMod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "user_app",
		Properties: map[string]interface{}{
			"name":        "user_app",
			"srcs":        []interface{}{"app.cpp"},
			"static_libs": []interface{}{"cycleA"},
		},
	}

	// 1. Order [cycleA, cycleB, userMod]: should fall back to cycleA, cycleB, then user_app
	sorted1 := gen.SortModules([]*eval.EvaluatedModule{cycleA, cycleB, userMod})
	if len(gen.CircularDeps) == 0 {
		t.Errorf("Expected circular dependencies recorded in CircularDeps")
	}
	if len(sorted1) != 3 {
		t.Fatalf("Expected 3 modules, got %d", len(sorted1))
	}
	if sorted1[0].Name != "cycleA" || sorted1[1].Name != "cycleB" || sorted1[2].Name != "user_app" {
		t.Errorf("Expected fallback order [cycleA, cycleB, user_app], got [%s, %s, %s]",
			sorted1[0].Name, sorted1[1].Name, sorted1[2].Name)
	}

	// 2. Order [cycleB, cycleA, userMod]: should fall back to cycleB, cycleA, then user_app
	gen2 := New(opts, nw, nil)
	sorted2 := gen2.SortModules([]*eval.EvaluatedModule{cycleB, cycleA, userMod})
	if len(sorted2) != 3 {
		t.Fatalf("Expected 3 modules, got %d", len(sorted2))
	}
	if sorted2[0].Name != "cycleB" || sorted2[1].Name != "cycleA" || sorted2[2].Name != "user_app" {
		t.Errorf("Expected fallback order [cycleB, cycleA, user_app], got [%s, %s, %s]",
			sorted2[0].Name, sorted2[1].Name, sorted2[2].Name)
	}
}

func TestMissingDependenciesWarning(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "test_bin",
		Properties: map[string]interface{}{
			"name":        "test_bin",
			"srcs":        []interface{}{"main.cpp"},
			"shared_libs": []interface{}{"libbase", "libc", "liblog"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	expectedMissing := []string{"libbase", "libc", "liblog"}
	if len(gen.MissingDeps) != len(expectedMissing) {
		t.Fatalf("Expected %d missing deps, got %d: %v", len(expectedMissing), len(gen.MissingDeps), gen.MissingDeps)
	}
	for i, m := range expectedMissing {
		if gen.MissingDeps[i] != m {
			t.Errorf("At index %d: expected %q, got %q", i, m, gen.MissingDeps[i])
		}
	}
}

func TestCircularDependencyWithExternalPrerequisiteAndDependent(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	// cycleB is declared first, cycleA declared second, leaf declared third, user declared fourth
	// cycleB <-> cycleA mutually depend on each other.
	// cycleA also depends on leaf.
	// user depends on cycleB.
	// Requirement: leaf MUST precede cycleB and cycleA. cycleB and cycleA must follow declaration order. user must follow the cycle.
	cycleB := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "cycleB",
		Properties: map[string]interface{}{
			"name":        "cycleB",
			"srcs":        []interface{}{"b.cpp"},
			"static_libs": []interface{}{"cycleA"},
		},
	}
	cycleA := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "cycleA",
		Properties: map[string]interface{}{
			"name":        "cycleA",
			"srcs":        []interface{}{"a.cpp"},
			"static_libs": []interface{}{"cycleB", "leaf"},
		},
	}
	leaf := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "leaf",
		Properties: map[string]interface{}{
			"name": "leaf",
			"srcs": []interface{}{"leaf.cpp"},
		},
	}
	user := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "user_app",
		Properties: map[string]interface{}{
			"name":        "user_app",
			"srcs":        []interface{}{"app.cpp"},
			"static_libs": []interface{}{"cycleB"},
		},
	}

	sorted := gen.SortModules([]*eval.EvaluatedModule{cycleB, cycleA, leaf, user})
	if len(sorted) != 4 {
		t.Fatalf("Expected 4 modules, got %d", len(sorted))
	}

	names := []string{sorted[0].Name, sorted[1].Name, sorted[2].Name, sorted[3].Name}
	// leaf must be first
	if names[0] != "leaf" {
		t.Errorf("Expected 'leaf' to precede cycle, got %s (full order: %v)", names[0], names)
	}
	// cycleB before cycleA (declaration order)
	if names[1] != "cycleB" || names[2] != "cycleA" {
		t.Errorf("Expected cycle to follow declaration order [cycleB, cycleA], got [%s, %s]", names[1], names[2])
	}
	// user_app must be last
	if names[3] != "user_app" {
		t.Errorf("Expected 'user_app' to succeed cycle, got %s", names[3])
	}
}

func TestDeepCycleFourModules(t *testing.T) {
	opts := DefaultOptions(".", "out")
	gen := New(opts, ninja.NewWriter(&bytes.Buffer{}), nil)

	// Cycle: mod0 -> mod1 -> mod2 -> mod3 -> mod0
	mod0 := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "mod0",
		Properties: map[string]interface{}{
			"name":        "mod0",
			"srcs":        []interface{}{"0.cpp"},
			"static_libs": []interface{}{"mod3"},
		},
	}
	mod1 := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "mod1",
		Properties: map[string]interface{}{
			"name":        "mod1",
			"srcs":        []interface{}{"1.cpp"},
			"static_libs": []interface{}{"mod0"},
		},
	}
	mod2 := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "mod2",
		Properties: map[string]interface{}{
			"name":        "mod2",
			"srcs":        []interface{}{"2.cpp"},
			"static_libs": []interface{}{"mod1"},
		},
	}
	mod3 := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "mod3",
		Properties: map[string]interface{}{
			"name":        "mod3",
			"srcs":        []interface{}{"3.cpp"},
			"static_libs": []interface{}{"mod2"},
		},
	}

	sorted := gen.SortModules([]*eval.EvaluatedModule{mod0, mod1, mod2, mod3})
	if len(sorted) != 4 {
		t.Fatalf("Expected 4 modules, got %d", len(sorted))
	}
	for i, expected := range []string{"mod0", "mod1", "mod2", "mod3"} {
		if sorted[i].Name != expected {
			t.Errorf("At index %d: expected %s, got %s", i, expected, sorted[i].Name)
		}
	}
}

func TestFilegroupSourcePathInSubdir(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	fg := &eval.EvaluatedModule{
		Type: "filegroup",
		Name: "my_fg",
		Dir:  "sub/dir",
		Properties: map[string]interface{}{
			"name": "my_fg",
			"srcs": []interface{}{"helper.cpp"},
		},
	}

	ccMod := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "libconsumer",
		Dir:  "other/dir",
		Properties: map[string]interface{}{
			"name": "libconsumer",
			"srcs": []interface{}{":my_fg", "main.cpp"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{fg, ccMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	// helper.cpp should be compiled from sub/dir/helper.cpp, NEVER other/dir/sub/dir/helper.cpp
	if strings.Contains(content, "other/dir/sub/dir/helper.cpp") {
		t.Errorf("Found duplicate prepended path in ninja output: %s", content)
	}
	if !strings.Contains(content, "sub/dir/helper.cpp") {
		t.Errorf("Expected sub/dir/helper.cpp in ninja output, got: %s", content)
	}
}

func TestCcLibraryPhonyWhenZeroObjsAndAllowMissingDeps(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	// Library with only a proto source (0 C/C++ objects)
	protoLib := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "libproto_only",
		Properties: map[string]interface{}{
			"name": "libproto_only",
			"srcs": []interface{}{"message.proto"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{protoLib}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	if !strings.Contains(content, "build out/lib64/libproto_only.a: phony") {
		t.Errorf("Expected phony rule for out/lib64/libproto_only.a, got:\n%s", content)
	}
}

func TestEnvironmentFlagOverridesInRules(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "my_bin",
		Properties: map[string]interface{}{
			"name": "my_bin",
			"srcs": []interface{}{"main.cpp"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	if !strings.Contains(content, "${CFLAGS}") || !strings.Contains(content, "${CCFLAGS}") {
		t.Errorf("Expected ${CFLAGS} and ${CCFLAGS} in cc_compile rule, got:\n%s", content)
	}
	if !strings.Contains(content, "${CXXFLAGS}") || !strings.Contains(content, "${CPPFLAGS}") {
		t.Errorf("Expected ${CXXFLAGS} and ${CPPFLAGS} in cxx_compile rule, got:\n%s", content)
	}
	if !strings.Contains(content, "${LDFLAGS}") {
		t.Errorf("Expected ${LDFLAGS} in link rules, got:\n%s", content)
	}
}

func TestKotlinPureCompilation(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "android_library",
		Name: "android_onboarding.common",
		Properties: map[string]interface{}{
			"name": "android_onboarding.common",
			"srcs": []interface{}{"Common.kt", "Utils.kt"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	if !strings.Contains(content, "build out/lib64/android_onboarding.common.jar: kotlinc_compile") {
		t.Errorf("Expected kotlinc_compile rule for pure-Kotlin module, got:\n%s", content)
	}
	if !strings.Contains(content, "$kotlinc -d out/classes/android_onboarding.common") {
		t.Errorf("Expected $kotlinc in command, got:\n%s", content)
	}
	if strings.Contains(content, "$javac") {
		t.Errorf("Did not expect $javac in pure-Kotlin module command, got:\n%s", content)
	}
}

func TestKotlinJavaMixedCompilation(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "mixed_lib",
		Properties: map[string]interface{}{
			"name":        "mixed_lib",
			"srcs":        []interface{}{"Foo.kt", "Bar.java"},
			"static_libs": []interface{}{"dep_lib"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	if !strings.Contains(content, "build out/lib64/mixed_lib.jar: kotlin_java_compile") {
		t.Errorf("Expected kotlin_java_compile rule for mixed module, got:\n%s", content)
	}
	if !strings.Contains(content, "$kotlinc") || !strings.Contains(content, "$javac") {
		t.Errorf("Expected both $kotlinc and $javac in mixed compilation command, got:\n%s", content)
	}
	if !strings.Contains(content, "out/lib64/dep_lib.jar") {
		t.Errorf("Expected dep_lib.jar in classpath/implicits, got:\n%s", content)
	}
}

func TestJavaPureCompilationWithClasspath(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "pure_java",
		Properties: map[string]interface{}{
			"name": "pure_java",
			"srcs": []interface{}{"Hello.java"},
			"libs": []interface{}{"some_api"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	if !strings.Contains(content, "build out/lib64/pure_java.jar: javac_compile") {
		t.Errorf("Expected javac_compile rule for pure Java module, got:\n%s", content)
	}
	if strings.Contains(content, "$kotlinc") {
		t.Errorf("Did not expect $kotlinc in pure Java module command, got:\n%s", content)
	}
	if !strings.Contains(content, "out/lib64/some_api.jar") {
		t.Errorf("Expected some_api.jar in classpath/implicits, got:\n%s", content)
	}
}

func TestJavaImport(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "java_import",
		Name: "prebuilt_dagger",
		Properties: map[string]interface{}{
			"name": "prebuilt_dagger",
			"jars": []interface{}{"libs/dagger-2.40.jar"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	if !strings.Contains(content, "build out/lib64/prebuilt_dagger.jar: copy libs/dagger-2.40.jar") {
		t.Errorf("Expected copy rule for java_import, got:\n%s", content)
	}
}



