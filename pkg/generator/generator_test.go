package generator

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ndk"
	"bp2ninja/pkg/ninja"
	"bp2ninja/pkg/parser"
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

	// Library with no C/C++ sources (0 C/C++ objects)
	emptyLib := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "libempty_only",
		Properties: map[string]interface{}{
			"name": "libempty_only",
			"srcs": []interface{}{"notes.txt"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{emptyLib}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	if !strings.Contains(content, "build out/lib64/libempty_only.a: phony") {
		t.Errorf("Expected phony rule for out/lib64/libempty_only.a, got:\n%s", content)
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

func TestGensrcsSingleAndMultiFile(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "gensrcs",
		Name: "art_operator_srcs",
		Properties: map[string]interface{}{
			"name":             "art_operator_srcs",
			"cmd":              "$(location generate_operator_out) $(in) > $(out)",
			"tools":            []interface{}{"generate_operator_out"},
			"srcs":             []interface{}{"arch/instruction_set.h", "base/allocator.h"},
			"output_extension": "operator_out.cc",
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	expectedOut1 := "out/gen/art_operator_srcs/arch/instruction_set.operator_out.cc"
	expectedOut2 := "out/gen/art_operator_srcs/base/allocator.operator_out.cc"

	if !strings.Contains(content, "build "+expectedOut1+": genrule_cmd arch/instruction_set.h") {
		t.Errorf("Expected build edge for %s, got:\n%s", expectedOut1, content)
	}
	if !strings.Contains(content, "build "+expectedOut2+": genrule_cmd base/allocator.h") {
		t.Errorf("Expected build edge for %s, got:\n%s", expectedOut2, content)
	}
	if !strings.Contains(content, "generate_operator_out arch/instruction_set.h > "+expectedOut1) {
		t.Errorf("Expected expanded cmd for first source, got:\n%s", content)
	}
	if !strings.Contains(content, "generate_operator_out base/allocator.h > "+expectedOut2) {
		t.Errorf("Expected expanded cmd for second source, got:\n%s", content)
	}
}

func TestGensrcsConsumerGeneratedSources(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	genMod := &eval.EvaluatedModule{
		Type: "gensrcs",
		Name: "my_gensrcs",
		Properties: map[string]interface{}{
			"name":             "my_gensrcs",
			"cmd":              "protoc $(in) -o $(out)",
			"srcs":             []interface{}{"foo.proto"},
			"output_extension": "cc",
		},
	}

	ccMod := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "libconsumer",
		Properties: map[string]interface{}{
			"name":              "libconsumer",
			"srcs":              []interface{}{"main.cpp"},
			"generated_sources": []interface{}{"my_gensrcs"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{genMod, ccMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	// Generated .cc source from my_gensrcs should be compiled into an object for libconsumer
	if !strings.Contains(content, "build out/obj/libconsumer/out/gen/my_gensrcs/foo.o: cxx_compile out/gen/my_gensrcs/foo.cc") &&
		!strings.Contains(content, "foo.o: cxx_compile out/gen/my_gensrcs/foo.cc") {
		t.Errorf("Expected compiled object for generated source foo.cc, got:\n%s", content)
	}
}

func TestGensrcsConsumerGeneratedHeaders(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	genMod := &eval.EvaluatedModule{
		Type: "gensrcs",
		Name: "proto_headers",
		Properties: map[string]interface{}{
			"name":                "proto_headers",
			"cmd":                 "gen_header $(in) $(out)",
			"srcs":                []interface{}{"api.proto"},
			"output_extension":    "h",
			"export_include_dirs": []interface{}{"."},
		},
	}

	ccMod := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "libuser",
		Properties: map[string]interface{}{
			"name":              "libuser",
			"srcs":              []interface{}{"user.cpp"},
			"generated_headers": []interface{}{"proto_headers"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{genMod, ccMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	// Check include path
	if !strings.Contains(content, "-Iout/gen/proto_headers") {
		t.Errorf("Expected -Iout/gen/proto_headers in compiler flags, got:\n%s", content)
	}
	// Check order-only dependency on generated header
	if !strings.Contains(content, "|| out/gen/proto_headers/api.h") {
		t.Errorf("Expected order-only dependency on out/gen/proto_headers/api.h, got:\n%s", content)
	}
}

func TestExcludeSrcsGlobAndFilegroup(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	fgMod := &eval.EvaluatedModule{
		Type: "filegroup",
		Name: "my_sources",
		Properties: map[string]interface{}{
			"name": "my_sources",
			"srcs": []interface{}{
				"mojo/core/channel.cc",
				"mojo/core/channel_unittest.cc",
				"mojo/public/js/bindings.js",
			},
			"exclude_srcs": []interface{}{
				"**/*_unittest.cc",
				"mojo/public/js/**/*",
			},
		},
	}

	ccMod := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "libchannel",
		Properties: map[string]interface{}{
			"name": "libchannel",
			"srcs": []interface{}{
				":my_sources",
				"extra.cpp",
				"extra_test.cpp",
			},
			"exclude_srcs": []interface{}{
				"*_test.cpp",
			},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{fgMod, ccMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	if !strings.Contains(content, "mojo/core/channel.cc") {
		t.Errorf("Expected mojo/core/channel.cc in build rules")
	}
	if strings.Contains(content, "channel_unittest.cc") {
		t.Errorf("channel_unittest.cc should have been excluded by filegroup exclude_srcs")
	}
	if strings.Contains(content, "bindings.js") {
		t.Errorf("bindings.js should have been excluded by filegroup exclude_srcs")
	}
	if !strings.Contains(content, "extra.cpp") {
		t.Errorf("Expected extra.cpp in build rules")
	}
	if strings.Contains(content, "extra_test.cpp") {
		t.Errorf("extra_test.cpp should have been excluded by cc_library exclude_srcs")
	}
}

func TestProtocRuleAndIncludes(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	protoLib := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "libproto_mod",
		Properties: map[string]interface{}{
			"name": "libproto_mod",
			"srcs": []interface{}{
				"protos/aconfig_storage_metadata.proto",
				"src/impl.cpp",
			},
		},
	}

	consumerBinary := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "aconfigd",
		Properties: map[string]interface{}{
			"name":        "aconfigd",
			"srcs":        []interface{}{"aconfigd.cpp"},
			"static_libs": []interface{}{"libproto_mod"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{protoLib, consumerBinary}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()

	// 1. Verify protoc rule syntax
	if !strings.Contains(content, "rule protoc") ||
		!strings.Contains(content, "command = mkdir -p $$(dirname $out) && $${PROTOC:-protoc} $protoc_args") {
		t.Errorf("Expected valid Ninja protoc rule with $$(dirname $out), got:\n%s", content)
	}

	// 2. Verify protoc edge preserving relative path
	expectedProtoOutCc := "out/gen/libproto_mod/protos/aconfig_storage_metadata.pb.cc"
	expectedProtoOutH := "out/gen/libproto_mod/protos/aconfig_storage_metadata.pb.h"
	if !strings.Contains(content, expectedProtoOutCc) || !strings.Contains(content, expectedProtoOutH) {
		t.Errorf("Expected protoc outputs for %s and %s, got:\n%s", expectedProtoOutCc, expectedProtoOutH, content)
	}

	// 3. Verify object compilation of the generated .pb.cc
	if !strings.Contains(content, "aconfig_storage_metadata.pb.o: cxx_compile "+expectedProtoOutCc) {
		t.Errorf("Expected cxx_compile for generated .pb.cc, got:\n%s", content)
	}

	// 4. Verify order-only dependency on .pb.h for local object and consumer object
	if !strings.Contains(content, "|| "+expectedProtoOutH) {
		t.Errorf("Expected order-only dependency on %s, got:\n%s", expectedProtoOutH, content)
	}

	// 5. Verify -Iout/gen/libproto_mod is passed to compiler includes
	if !strings.Contains(content, "-Iout/gen/libproto_mod") {
		t.Errorf("Expected -Iout/gen/libproto_mod in compiler includes, got:\n%s", content)
	}
}

func TestCurrentAndroidJarResolution(t *testing.T) {
	tmpDir := t.TempDir()
	p34 := filepath.Join(tmpDir, "platforms/android-34")
	p32 := filepath.Join(tmpDir, "platforms/android-32")
	if err := os.MkdirAll(p34, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p32, 0755); err != nil {
		t.Fatal(err)
	}
	jar34 := filepath.Join(p34, "android.jar")
	jar32 := filepath.Join(p32, "android.jar")
	if err := os.WriteFile(jar34, []byte("jar34"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jar32, []byte("jar32"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ANDROID_SDK", tmpDir)
	t.Setenv("ANDROID_HOME", "")

	// 1. Exact match APILevel 34
	var buf1 bytes.Buffer
	nw1 := ninja.NewWriter(&buf1)
	opts1 := DefaultOptions(".", "out")
	opts1.APILevel = 34
	gen1 := New(opts1, nw1, nil)

	resolvedJar1 := gen1.resolveCurrentAndroidJar()
	if resolvedJar1 != jar34 {
		t.Errorf("Expected resolved android.jar for API 34 to be %s, got %s", jar34, resolvedJar1)
	}

	// 2. Exact match APILevel 32
	var buf2 bytes.Buffer
	nw2 := ninja.NewWriter(&buf2)
	opts2 := DefaultOptions(".", "out")
	opts2.APILevel = 32
	gen2 := New(opts2, nw2, nil)

	resolvedJar2 := gen2.resolveCurrentAndroidJar()
	if resolvedJar2 != jar32 {
		t.Errorf("Expected resolved android.jar for API 32 to be %s, got %s", jar32, resolvedJar2)
	}

	// 3. Resolve :current_android_jar in java_library srcs
	javaMod := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "my_java_lib",
		Properties: map[string]interface{}{
			"name": "my_java_lib",
			"srcs": []interface{}{
				"Foo.java",
				":current_android_jar",
			},
		},
	}
	if err := gen1.Generate([]*eval.EvaluatedModule{javaMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content1 := buf1.String()
	if !strings.Contains(content1, jar34) {
		t.Errorf("Expected ninja output to contain resolved jar path %s, got:\n%s", jar34, content1)
	}

	// 4. Resolve :current_android_jar in genrule $(location :current_android_jar)
	var buf3 bytes.Buffer
	nw3 := ninja.NewWriter(&buf3)
	opts3 := DefaultOptions(".", "out")
	opts3.APILevel = 34
	gen3 := New(opts3, nw3, nil)

	genMod := &eval.EvaluatedModule{
		Type: "genrule",
		Name: "my_genrule",
		Properties: map[string]interface{}{
			"name": "my_genrule",
			"srcs": []interface{}{":current_android_jar"},
			"out":  []interface{}{"out.h"},
			"cmd":  "process $(location :current_android_jar) $(out)",
		},
	}
	if err := gen3.Generate([]*eval.EvaluatedModule{genMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content3 := buf3.String()
	if !strings.Contains(content3, "process "+jar34+" out/gen/my_genrule/out.h") {
		t.Errorf("Expected genrule cmd to expand $(location :current_android_jar) to %s, got:\n%s", jar34, content3)
	}
}

func TestGensrcsDataColonReference(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "gensrcs",
		Name: "framework-javastream-protos",
		Properties: map[string]interface{}{
			"name":             "framework-javastream-protos",
			"srcs":             []interface{}{"proto/ipconnectivity.proto"},
			"output_extension": "srcjar",
			"data":             []interface{}{":libprotobuf-internal-protos"},
			"cmd":              "protoc $(in) -o $(out)",
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	// Ninja prerequisite should NOT have leading colon
	if strings.Contains(content, ":libprotobuf-internal-protos") {
		t.Errorf("Ninja output should not contain raw colon reference :libprotobuf-internal-protos, got:\n%s", content)
	}
	// It should contain resolved reference and phony target for missing dep
	if !strings.Contains(content, "| libprotobuf-internal-protos") {
		t.Errorf("Expected implicit dependency on libprotobuf-internal-protos, got:\n%s", content)
	}
	if !strings.Contains(content, "build libprotobuf-internal-protos: phony") {
		t.Errorf("Expected phony target for libprotobuf-internal-protos, got:\n%s", content)
	}
}

func TestJavaAconfigLibraryPhony(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "java_aconfig_library",
		Name: "easter_egg_flags_lib",
		Properties: map[string]interface{}{
			"name":                 "easter_egg_flags_lib",
			"aconfig_declarations": "easter_egg_flags",
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	expectedJar := filepath.Join("out/lib64", "easter_egg_flags_lib.jar")
	if !strings.Contains(content, "build "+expectedJar+": phony") {
		t.Errorf("Expected phony target for %s, got:\n%s", expectedJar, content)
	}
}

func TestCcAconfigLibrary(t *testing.T) {
	tmpDir := t.TempDir()
	outDir := filepath.Join(tmpDir, "out")
	aconfigDir := filepath.Join(tmpDir, "services", "backup")
	if err := os.MkdirAll(aconfigDir, 0755); err != nil {
		t.Fatalf("Failed to create aconfigDir: %v", err)
	}
	aconfigContent := `
package: "com.android.server.backup"

flag {
    name: "enable_max_size_writes_to_pipes"
    namespace: "onboarding"
    description: "Enables the write buffer to pipes to be of maximum size."
    bug: "265976737"
    is_fixed_read_only: true
}
`
	if err := os.WriteFile(filepath.Join(aconfigDir, "flags.aconfig"), []byte(aconfigContent), 0644); err != nil {
		t.Fatalf("Failed to write flags.aconfig: %v", err)
	}

	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tmpDir, outDir)
	opts.BpDir = tmpDir
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	declMod := &eval.EvaluatedModule{
		Type: "aconfig_declarations",
		Name: "backup_flags",
		Dir:  "services/backup",
		Properties: map[string]interface{}{
			"name":    "backup_flags",
			"package": "com.android.server.backup",
			"srcs":    []interface{}{"flags.aconfig"},
		},
	}

	ccAconfigMod := &eval.EvaluatedModule{
		Type: "cc_aconfig_library",
		Name: "backup_flags_cc_lib",
		Properties: map[string]interface{}{
			"name":                 "backup_flags_cc_lib",
			"aconfig_declarations": "backup_flags",
		},
	}

	ccLibMod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libandroidfw",
		Properties: map[string]interface{}{
			"name":        "libandroidfw",
			"srcs":        []interface{}{"Util.cpp"},
			"static_libs": []interface{}{"backup_flags_cc_lib"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{declMod, ccAconfigMod, ccLibMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// 1. Verify generated header exists and has correct content
	hdrPath := filepath.Join(outDir, "gen", "backup_flags_cc_lib", "include", "com_android_server_backup.h")
	hdrBytes, err := os.ReadFile(hdrPath)
	if err != nil {
		t.Fatalf("Expected generated header at %s, got error: %v", hdrPath, err)
	}
	hdrStr := string(hdrBytes)
	if !strings.Contains(hdrStr, "namespace com::android::server::backup") {
		t.Errorf("Expected namespace com::android::server::backup in header, got:\n%s", hdrStr)
	}
	if !strings.Contains(hdrStr, "inline bool enable_max_size_writes_to_pipes()") {
		t.Errorf("Expected inline bool enable_max_size_writes_to_pipes() in header, got:\n%s", hdrStr)
	}

	// 2. Verify Ninja build file contains include directory for libandroidfw
	content := buf.String()
	expectedInc := "-I" + filepath.Join(outDir, "gen", "backup_flags_cc_lib", "include")
	if !strings.Contains(content, expectedInc) {
		t.Errorf("Expected %s in Ninja rules for libandroidfw, got:\n%s", expectedInc, content)
	}

	// 3. Verify static archive target for backup_flags_cc_lib
	expectedStatic := filepath.Join(outDir, "lib64", "libbackup_flags_cc_lib.a")
	if !strings.Contains(content, "build "+expectedStatic+": archive_static") {
		t.Errorf("Expected archive_static for %s, got:\n%s", expectedStatic, content)
	}
}

func TestTaggedModuleReferenceResolution(t *testing.T) {
	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	genMod := &eval.EvaluatedModule{
		Type: "genrule",
		Name: "framework-minus-apex.ravenwood-base",
		Properties: map[string]interface{}{
			"name": "framework-minus-apex.ravenwood-base",
			"out":  []interface{}{"ravenwood.jar", "ravenwood-data.txt"},
			"cmd":  "echo test > $(out)",
		},
	}

	consumerMod := &eval.EvaluatedModule{
		Type: "genrule",
		Name: "consumer",
		Properties: map[string]interface{}{
			"name": "consumer",
			"srcs": []interface{}{":framework-minus-apex.ravenwood-base{ravenwood.jar}"},
			"out":  []interface{}{"final.out"},
			"cmd":  "cat $(in) > $(out)",
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{genMod, consumerMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	expectedInput := filepath.Join("out", "gen", "framework-minus-apex.ravenwood-base", "ravenwood.jar")
	if !strings.Contains(content, expectedInput) {
		t.Errorf("Expected consumer rule to depend on %s, got:\n%s", expectedInput, content)
	}
	unexpectedInput := filepath.Join("out", "gen", "framework-minus-apex.ravenwood-base", "ravenwood-data.txt")
	if strings.Contains(content, "genrule_cmd "+expectedInput+" "+unexpectedInput) {
		t.Errorf("Consumer rule should not include non-tagged output %s", unexpectedInput)
	}
}

func TestRootTargetDefaultBounding(t *testing.T) {
	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	rootMod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "my_main_tool",
		Dir:  "",
		Properties: map[string]interface{}{
			"name": "my_main_tool",
			"srcs": []interface{}{"main.cpp"},
		},
	}

	childMod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "child_sub_tool",
		Dir:  "sub/dir",
		Properties: map[string]interface{}{
			"name": "child_sub_tool",
			"srcs": []interface{}{"child.cpp"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{rootMod, childMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	mainTarget := filepath.Join("out", "bin", "my_main_tool")
	childTarget := filepath.Join("out", "bin", "child_sub_tool")

	if !strings.Contains(content, "default "+mainTarget) {
		t.Errorf("Expected default directive to contain %s, got:\n%s", mainTarget, content)
	}
	if strings.Contains(content, "default "+mainTarget+" "+childTarget) {
		t.Errorf("Default directive should NOT contain child module target %s when root targets exist", childTarget)
	}
}

func TestCcBinaryMassSymlinks(t *testing.T) {
	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	toyboxMod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "toybox",
		Properties: map[string]interface{}{
			"name":     "toybox",
			"srcs":     []interface{}{"main.c"},
			"symlinks": []interface{}{"cat", "ls", "grep"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{toyboxMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()

	// Verify rule symlink is emitted
	if !strings.Contains(content, "rule symlink") {
		t.Errorf("Expected 'rule symlink' in build.ninja, got:\n%s", content)
	}

	// Verify symlink build edges
	for _, alias := range []string{"cat", "ls", "grep"} {
		expectedEdge := "build " + filepath.Join("out", "bin", alias) + ": symlink " + filepath.Join("out", "bin", "toybox")
		if !strings.Contains(content, expectedEdge) {
			t.Errorf("Expected edge %q, got:\n%s", expectedEdge, content)
		}
		expectedTarget := "symlink_target = toybox"
		if !strings.Contains(content, expectedTarget) {
			t.Errorf("Expected variable %q, got:\n%s", expectedTarget, content)
		}
	}
}

func TestSymlinkPreferredArch(t *testing.T) {
	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	appProcessMod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "app_process64",
		Properties: map[string]interface{}{
			"name":                   "app_process64",
			"srcs":                   []interface{}{"app_main.cpp"},
			"symlink_preferred_arch": true,
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{appProcessMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	expectedEdge := "build " + filepath.Join("out", "bin", "app_process") + ": symlink " + filepath.Join("out", "bin", "app_process64")
	if !strings.Contains(content, expectedEdge) {
		t.Errorf("Expected edge %q, got:\n%s", expectedEdge, content)
	}
	expectedTarget := "symlink_target = app_process64"
	if !strings.Contains(content, expectedTarget) {
		t.Errorf("Expected variable %q, got:\n%s", expectedTarget, content)
	}
}

func TestInstallSymlink(t *testing.T) {
	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	symMod := &eval.EvaluatedModule{
		Type: "install_symlink",
		Name: "linker_asan_link",
		Properties: map[string]interface{}{
			"name":               "linker_asan_link",
			"installed_location": "bin/linker_asan",
			"symlink_target":     "/apex/com.android.runtime/bin/linker",
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{symMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	expectedEdge := "build " + filepath.Join("out", "bin", "linker_asan") + ": symlink"
	if !strings.Contains(content, expectedEdge) {
		t.Errorf("Expected edge %q, got:\n%s", expectedEdge, content)
	}
	expectedTarget := "symlink_target = /apex/com.android.runtime/bin/linker"
	if !strings.Contains(content, expectedTarget) {
		t.Errorf("Expected variable %q, got:\n%s", expectedTarget, content)
	}
}

func TestPrebuiltMassSymlinks(t *testing.T) {
	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	shMod := &eval.EvaluatedModule{
		Type: "sh_binary",
		Name: "myscript",
		Properties: map[string]interface{}{
			"name":     "myscript",
			"src":      "myscript.sh",
			"symlinks": []interface{}{"alias_script"},
		},
	}

	etcMod := &eval.EvaluatedModule{
		Type: "prebuilt_etc",
		Name: "myconfig",
		Properties: map[string]interface{}{
			"name":     "myconfig",
			"src":      "myconfig.conf",
			"sub_dir":  "settings",
			"symlinks": []interface{}{"alias_conf"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{shMod, etcMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	expectedSh := "build " + filepath.Join("out", "bin", "alias_script") + ": symlink " + filepath.Join("out", "bin", "myscript")
	if !strings.Contains(content, expectedSh) {
		t.Errorf("Expected edge %q, got:\n%s", expectedSh, content)
	}
	expectedEtc := "build " + filepath.Join("out", "etc", "settings", "alias_conf") + ": symlink " + filepath.Join("out", "etc", "settings", "myconfig")
	if !strings.Contains(content, expectedEtc) {
		t.Errorf("Expected edge %q, got:\n%s", expectedEtc, content)
	}
}

func TestArtCcLibraryNinjaGeneration(t *testing.T) {
	bp := `
art_cc_defaults {
    name: "vixl-arm",
    codegen: {
        arm: {
            srcs: ["src/aarch32/assembler-aarch32.cc"],
            cppflags: ["-DVIXL_INCLUDE_TARGET_T32"],
        },
    },
}

art_cc_defaults {
    name: "vixl-arm64",
    codegen: {
        arm64: {
            srcs: ["src/aarch64/assembler-aarch64.cc"],
            cppflags: ["-DVIXL_INCLUDE_SIMULATOR_AARCH64", "-DVIXL_INCLUDE_TARGET_A64"],
        },
    },
}

art_cc_library {
    name: "libvixl",
    defaults: ["vixl-arm", "vixl-arm64"],
    srcs: ["src/code-buffer-vixl.cc"],
}
`
	ctx := eval.NewContext(nil, "x86_64")
	// Target is x86_64 with ART_TARGET_CODEGEN_ARCHS enabling ARM and ARM64
	ctx.TargetCodegenArches = []string{"arm", "arm64", "x86", "x86_64"}
	ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}
	if err := ctx.EvalFileInDir(ast, ""); err != nil {
		t.Fatalf("EvalFileInDir failed: %v", err)
	}

	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.TargetArch = "x86_64"
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	if err := gen.Generate(ctx.Modules); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()

	// Verify libvixl.so and libvixl.a are real targets with link_shared and archive_static
	expectedShared := filepath.Join("out", "lib64", "libvixl.so")
	expectedStatic := filepath.Join("out", "lib64", "libvixl.a")
	if !strings.Contains(content, "build "+expectedShared+": link_shared") {
		t.Errorf("Expected link_shared for %s, got:\n%s", expectedShared, content)
	}
	if !strings.Contains(content, "build "+expectedStatic+": archive_static") {
		t.Errorf("Expected archive_static for %s, got:\n%s", expectedStatic, content)
	}

	// Verify that phony fallbacks are NOT emitted for libvixl
	if strings.Contains(content, "build "+expectedShared+": phony") {
		t.Errorf("Did not expect phony rule for %s", expectedShared)
	}
	if strings.Contains(content, "build "+expectedStatic+": phony") {
		t.Errorf("Did not expect phony rule for %s", expectedStatic)
	}

	// Verify compilation of AArch64 simulator sources
	expectedA64Obj := filepath.Join("out", "obj", "libvixl", "src", "aarch64", "assembler-aarch64.o")
	if !strings.Contains(content, "build "+expectedA64Obj+": cxx_compile") {
		t.Errorf("Expected compile rule for %s, got:\n%s", expectedA64Obj, content)
	}
}

func TestAssemblerPipelineAndAsflags(t *testing.T) {
	bp := `
cc_library_static {
    name: "libbionic_asm",
    srcs: ["bionic.c", "arch_arm64.S"],
    cflags: ["-include freebsd-compat.h"],
    asflags: ["-D__ASSEMBLY__", "-DTEST_ASM_FLAG"],
}
`
	ctx := eval.NewContext(nil, "arm64")
	ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}
	if err := ctx.EvalFileInDir(ast, ""); err != nil {
		t.Fatalf("EvalFileInDir failed: %v", err)
	}

	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)
	if err := gen.Generate(ctx.Modules); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()

	// 1. Verify as_compile rule exists
	if !strings.Contains(content, "rule as_compile") {
		t.Fatalf("Expected rule as_compile definition, got:\n%s", content)
	}

	// 2. Verify bionic.c uses cc_compile and has -include freebsd-compat.h
	cObj := filepath.Join("out", "obj", "libbionic_asm", "bionic.o")
	if !strings.Contains(content, "build "+cObj+": cc_compile bionic.c") {
		t.Errorf("Expected cc_compile for %s, got:\n%s", cObj, content)
	}
	if !strings.Contains(content, "-include freebsd-compat.h") {
		t.Errorf("Expected -include freebsd-compat.h in cflags")
	}

	// 3. Verify arch_arm64.S uses as_compile and has asflags with -D__ASSEMBLY__ and -DTEST_ASM_FLAG
	sObj := filepath.Join("out", "obj", "libbionic_asm", "arch_arm64.o")
	if !strings.Contains(content, "build "+sObj+": as_compile arch_arm64.S") {
		t.Errorf("Expected as_compile for %s, got:\n%s", sObj, content)
	}
	if !strings.Contains(content, "asflags = ") || !strings.Contains(content, "-DTEST_ASM_FLAG") {
		t.Errorf("Expected -DTEST_ASM_FLAG in asflags, got:\n%s", content)
	}

	// 4. Verify that .S build edge does NOT contain C-specific -include freebsd-compat.h
	edgeIdx := strings.Index(content, "build "+sObj+": as_compile")
	if edgeIdx != -1 {
		edgeSlice := content[edgeIdx:]
		nextEdge := strings.Index(edgeSlice[1:], "\nbuild ")
		if nextEdge != -1 {
			edgeSlice = edgeSlice[:nextEdge+1]
		}
		if strings.Contains(edgeSlice, "freebsd-compat.h") {
			t.Errorf("Assembly build edge should not contain C headers, got:\n%s", edgeSlice)
		}
	}
}

func TestCcObjectAndPartialLink(t *testing.T) {
	bp := `
cc_object {
    name: "crtbrand",
    srcs: ["crtbrand.S"],
}

cc_object {
    name: "crtbegin_so",
    srcs: ["crtbegin.c"],
    objs: ["crtbrand"],
}
`
	ctx := eval.NewContext(nil, "arm64")
	ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}
	if err := ctx.EvalFileInDir(ast, ""); err != nil {
		t.Fatalf("EvalFileInDir failed: %v", err)
	}

	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)
	if err := gen.Generate(ctx.Modules); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()

	// 1. Verify partial_link rule exists
	if !strings.Contains(content, "rule partial_link") {
		t.Fatalf("Expected rule partial_link definition, got:\n%s", content)
	}

	// 2. Verify crtbrand.S is compiled with as_compile
	brandObjSrc := filepath.Join("out", "obj", "crtbrand.src", "crtbrand.o")
	if !strings.Contains(content, "build "+brandObjSrc+": as_compile crtbrand.S") {
		t.Errorf("Expected as_compile for %s, got:\n%s", brandObjSrc, content)
	}

	// 3. Verify crtbegin_so.o uses partial_link combining crtbegin.o and crtbrand.o
	crtbeginFinal := filepath.Join("out", "obj", "crtbegin_so", "crtbegin_so.o")
	if !strings.Contains(content, "build "+crtbeginFinal+": partial_link") {
		t.Errorf("Expected partial_link for %s, got:\n%s", crtbeginFinal, content)
	}
}

func TestCompositeStaticArchiveMRI(t *testing.T) {
	bp := `
cc_library_static {
    name: "libc_bionic",
    srcs: ["bionic.c"],
}

cc_library_static {
    name: "libc_dns",
    srcs: ["dns.c"],
}

cc_library_static {
    name: "libc",
    whole_static_libs: ["libc_bionic", "libc_dns"],
}
`
	ctx := eval.NewContext(nil, "arm64")
	ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}
	if err := ctx.EvalFileInDir(ast, ""); err != nil {
		t.Fatalf("EvalFileInDir failed: %v", err)
	}

	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)
	if err := gen.Generate(ctx.Modules); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()

	// 1. Verify ar_mri rule exists
	if !strings.Contains(content, "rule ar_mri") {
		t.Fatalf("Expected rule ar_mri definition, got:\n%s", content)
	}

	// 2. Verify libc.a is generated using ar_mri
	libcArchive := filepath.Join("out", "lib64", "libc.a")
	if !strings.Contains(content, "build "+libcArchive+": ar_mri") {
		t.Errorf("Expected ar_mri for %s, got:\n%s", libcArchive, content)
	}

	// 3. Verify MRI script contains ADDLIB for both sub-archives
	bionicArchive := filepath.Join("out", "lib64", "libc_bionic.a")
	dnsArchive := filepath.Join("out", "lib64", "libc_dns.a")
	if !strings.Contains(content, "ADDLIB "+bionicArchive) {
		t.Errorf("Expected ADDLIB %s in MRI script, got:\n%s", bionicArchive, content)
	}
	if !strings.Contains(content, "ADDLIB "+dnsArchive) {
		t.Errorf("Expected ADDLIB %s in MRI script, got:\n%s", dnsArchive, content)
	}
}

func TestBareMetalLinker64(t *testing.T) {
	bp := `
cc_binary {
    name: "linker",
    stem: "linker64",
    srcs: ["linker_main.cpp"],
    nocrt: true,
    ldflags: ["-shared", "-Wl,-Bsymbolic", "-Wl,--exclude-libs,ALL", "-Wl,-soname,ld-android.so"],
    static_libs: ["libc_nomalloc"],
}
`
	ctx := eval.NewContext(nil, "arm64")
	ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}
	if err := ctx.EvalFileInDir(ast, ""); err != nil {
		t.Fatalf("EvalFileInDir failed: %v", err)
	}

	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)
	if err := gen.Generate(ctx.Modules); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()

	// 1. Output binary should respect stem "linker64"
	expectedTarget := filepath.Join("out", "bin", "linker64")
	if !strings.Contains(content, "build "+expectedTarget+": link_binary") {
		t.Errorf("Expected link_binary for %s, got:\n%s", expectedTarget, content)
	}

	// 2. Verify bare-metal ldflags: -nostartfiles and -Wl,-Bsymbolic
	if !strings.Contains(content, "-nostartfiles") {
		t.Errorf("Expected -nostartfiles in ldflags due to nocrt: true, got:\n%s", content)
	}
	if !strings.Contains(content, "-Wl,-Bsymbolic") {
		t.Errorf("Expected -Wl,-Bsymbolic in ldflags, got:\n%s", content)
	}
}

func TestNdkLibraryVersionScript(t *testing.T) {
	bp := `
ndk_library {
    name: "libc",
    symbol_file: "libc.map.txt",
    first_version: "9",
}

cc_library {
    name: "libc",
    srcs: ["libc_dummy.c"],
}
`
	ctx := eval.NewContext(nil, "arm64")
	ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}
	if err := ctx.EvalFileInDir(ast, ""); err != nil {
		t.Fatalf("EvalFileInDir failed: %v", err)
	}

	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)
	if err := gen.Generate(ctx.Modules); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()

	// 1. Verify gen_version_script rule exists
	if !strings.Contains(content, "rule gen_version_script") {
		t.Fatalf("Expected rule gen_version_script definition, got:\n%s", content)
	}

	// 2. Verify version_script output target
	vsOut := filepath.Join("out", "gen", "libc", "libc.version_script")
	if !strings.Contains(content, "build "+vsOut+": gen_version_script libc.map.txt") {
		t.Errorf("Expected gen_version_script for %s, got:\n%s", vsOut, content)
	}

	// 3. Verify companion cc_library links with -Wl,--version-script
	if !strings.Contains(content, "-Wl,--version-script="+vsOut) {
		t.Errorf("Expected companion cc_library to include version script flag, got:\n%s", content)
	}
}

func TestIncludeRootPathResolution(t *testing.T) {
	tmpDir := t.TempDir()
	mockHdrDir := filepath.Join(tmpDir, "bionic", "libc", "include")
	if err := os.MkdirAll(mockHdrDir, 0755); err != nil {
		t.Fatalf("Failed to create mock dir: %v", err)
	}

	bp := `
cc_library_static {
    name: "libwith_root_includes",
    srcs: ["test.c"],
    include_dirs: ["bionic/libc/include"],
    cflags: ["-Ibionic/libc/include"],
}
`
	ctx := eval.NewContext(nil, "arm64")
	ast, errs := parser.Parse("Android.bp", strings.NewReader(bp), parser.NewScope(nil))
	if len(errs) > 0 {
		t.Fatalf("Parse error: %v", errs)
	}
	if err := ctx.EvalFileInDir(ast, ""); err != nil {
		t.Fatalf("EvalFileInDir failed: %v", err)
	}

	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.IncludeRoot = tmpDir
	gen := New(opts, nw, nil)
	if err := gen.Generate(ctx.Modules); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()

	// Verify that the include path was resolved to the mock root directory
	expectedInc := "-I" + mockHdrDir
	if !strings.Contains(content, expectedInc) {
		t.Errorf("Expected %s in include paths, got:\n%s", expectedInc, content)
	}
}

func TestLibDir32Vs64Bit(t *testing.T) {
	opts64 := DefaultOptions(".", "out")
	opts64.TargetArch = "x86_64"
	gen64 := New(opts64, nil, nil)
	if gen64.libDir() != filepath.Join("out", "lib64") {
		t.Errorf("Expected out/lib64 for x86_64, got %s", gen64.libDir())
	}

	opts32 := DefaultOptions(".", "out")
	opts32.TargetArch = "x86"
	gen32 := New(opts32, nil, nil)
	if gen32.libDir() != filepath.Join("out", "lib") {
		t.Errorf("Expected out/lib for x86, got %s", gen32.libDir())
	}

	optsArm := DefaultOptions(".", "out")
	optsArm.TargetArch = "arm"
	genArm := New(optsArm, nil, nil)
	if genArm.libDir() != filepath.Join("out", "lib") {
		t.Errorf("Expected out/lib for arm, got %s", genArm.libDir())
	}
}

func TestX32ArchSupport(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.TargetArch = "x32"
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libx32test",
		Properties: map[string]interface{}{
			"name": "libx32test",
			"srcs": []interface{}{"a.c", "b.S", "c.cpp"},
		},
	}
	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	ninjaContent := buf.String()

	// -mx32 must reach C, C++ and assembly compile flags
	ccIdx := strings.Index(ninjaContent, "build out/obj/libx32test/a.o: cc_compile")
	if ccIdx < 0 {
		t.Fatalf("Expected cc_compile edge for a.c")
	}
	if !strings.Contains(ninjaContent[ccIdx:], "-mx32") {
		t.Errorf("Expected -mx32 in C compile flags")
	}
	cxxIdx := strings.Index(ninjaContent, "build out/obj/libx32test/c.o: cxx_compile")
	if cxxIdx < 0 {
		t.Fatalf("Expected cxx_compile edge for c.cpp")
	}
	if !strings.Contains(ninjaContent[cxxIdx:], "-mx32") {
		t.Errorf("Expected -mx32 in C++ compile flags")
	}
	asIdx := strings.Index(ninjaContent, "build out/obj/libx32test/b.o: as_compile")
	if asIdx < 0 {
		t.Fatalf("Expected as_compile edge for b.S")
	}
	if !strings.Contains(ninjaContent[asIdx:], "-mx32") {
		t.Errorf("Expected -mx32 in assembly flags")
	}

	// Link flags and glibc multilib layout
	if !strings.Contains(ninjaContent, "out/libx32/libx32test.so") {
		t.Errorf("Expected out/libx32/ output layout for x32")
	}
	linkIdx := strings.Index(ninjaContent, "out/libx32/libx32test.so: link_shared")
	if linkIdx < 0 {
		t.Fatalf("Expected link_shared edge for libx32test.so")
	}
	if !strings.Contains(ninjaContent[linkIdx:], "-mx32") {
		t.Errorf("Expected -mx32 in link flags")
	}

	// Triple mapping for the x32 ABI
	if got := ndk.ArchitectureToTriple("x32"); got != "x86_64-linux-gnux32" {
		t.Errorf("Expected x86_64-linux-gnux32 triple for x32, got %s", got)
	}
}

func TestSisoRuleBindings(t *testing.T) {
	newOutput := func(siso, sisoRemote bool) string {
		var buf bytes.Buffer
		nw := ninja.NewWriter(&buf)
		opts := DefaultOptions(".", "out")
		opts.AllowMissingDeps = true
		opts.Siso = siso
		opts.SisoRemote = sisoRemote
		gen := New(opts, nw, nil)
		mod := &eval.EvaluatedModule{
			Type: "cc_library",
			Name: "libsiso",
			Properties: map[string]interface{}{
				"name": "libsiso",
				"srcs": []interface{}{"a.c"},
			},
		}
		if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
			t.Fatalf("Generate failed: %v", err)
		}
		return buf.String()
	}

	// Without -siso: no Siso bindings anywhere (upstream ninja compat)
	plain := newOutput(false, false)
	for _, binding := range []string{"restat_content", "remote_enabled", "sandbox_disabled", "cache_disabled", "phony_output"} {
		if strings.Contains(plain, binding) {
			t.Errorf("Expected no %s binding without -siso", binding)
		}
	}

	// With -siso: deterministic rules get restat_content, host-tool bash rules
	// get sandbox_disabled, link rules stay local
	sisoOut := newOutput(true, false)
	if !strings.Contains(sisoOut, "restat_content = 1") {
		t.Errorf("Expected restat_content bindings with -siso")
	}
	if !strings.Contains(sisoOut, "sandbox_disabled = true") {
		t.Errorf("Expected sandbox_disabled bindings with -siso")
	}
	if strings.Contains(sisoOut, "remote_enabled") {
		t.Errorf("remote_enabled must not be emitted without -siso-remote")
	}
	for _, rule := range []string{"protoc", "genrule_cmd", "ar_mri", "gen_version_script", "archive_static", "symlink", "copy\n"} {
		ruleIdx := strings.Index(sisoOut, "rule "+strings.TrimSpace(rule))
		if ruleIdx < 0 {
			continue // rule not emitted for this module set
		}
		nextRule := strings.Index(sisoOut[ruleIdx+1:], "\nrule ")
		section := sisoOut[ruleIdx:]
		if nextRule >= 0 {
			section = sisoOut[ruleIdx : ruleIdx+1+nextRule]
		}
		if !strings.Contains(section, "restat_content = 1") {
			t.Errorf("Expected restat_content on rule %s", strings.TrimSpace(rule))
		}
	}

	// With -siso-remote: pure compile actions are remote_enabled
	remoteOut := newOutput(true, true)
	for _, rule := range []string{"cc_compile", "cxx_compile", "as_compile"} {
		ruleIdx := strings.Index(remoteOut, "rule "+rule+"\n")
		if ruleIdx < 0 {
			t.Fatalf("Expected rule %s emitted", rule)
		}
		nextRule := strings.Index(remoteOut[ruleIdx+1:], "\nrule ")
		section := remoteOut[ruleIdx:]
		if nextRule >= 0 {
			section = remoteOut[ruleIdx : ruleIdx+1+nextRule]
		}
		if !strings.Contains(section, "remote_enabled = true") {
			t.Errorf("Expected remote_enabled on rule %s with -siso-remote", rule)
		}
	}
	linkIdx := strings.Index(remoteOut, "rule link_shared")
	if linkIdx >= 0 && strings.Contains(remoteOut[linkIdx:linkIdx+400], "remote_enabled") {
		t.Errorf("link_shared must stay local")
	}
}

func TestWriteSisoConfig(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.Siso = true
	opts.NDKDir = "/opt/android-ndk"
	opts.ClangPath = "/opt/android-ndk/toolchains/llvm/prebuilt/linux-x86_64/bin/x86_64-linux-android34-clang"
	opts.SysrootDir = "/opt/android-ndk/toolchains/llvm/prebuilt/linux-x86_64/sysroot"
	gen := New(opts, nw, nil)

	var star bytes.Buffer
	if err := gen.WriteSisoConfig(&star); err != nil {
		t.Fatalf("WriteSisoConfig failed: %v", err)
	}
	content := star.String()
	for _, fragment := range []string{
		"def init(ctx):",
		`load("@builtin//encoding.star", "json")`,
		`load("@builtin//struct.star", "module")`,
		"step_config = json.encode(step_config)",
		"handlers = {},",
		"filegroups = {},",
		`"action": "^(link_shared|link_binary|partial_link|archive_static|ar_mri)$"`,
		`"action": "^(cc_compile|cxx_compile|as_compile)$"`,
		`"input_root_absolute_path": True,`,
	} {
		if !strings.Contains(content, fragment) {
			t.Errorf("Expected %q in generated siso.star", fragment)
		}
	}
	if !strings.Contains(content, "/opt/android-ndk/toolchains/llvm/prebuilt/linux-x86_64/bin") {
		t.Errorf("Expected NDK toolchain hook comment")
	}
	if !strings.Contains(content, "/opt/android-ndk/toolchains/llvm/prebuilt/linux-x86_64/sysroot") {
		t.Errorf("Expected sysroot in toolchain hook")
	}
}

func TestTargetTripleInjectionForPlainCompilers(t *testing.T) {
	genOutput := func(mutate func(*Options)) string {
		var buf bytes.Buffer
		nw := ninja.NewWriter(&buf)
		opts := DefaultOptions(".", "out")
		opts.AllowMissingDeps = true
		mutate(&opts)
		gen := New(opts, nw, nil)
		mod := &eval.EvaluatedModule{
			Type: "cc_library",
			Name: "libtriple",
			Properties: map[string]interface{}{
				"name": "libtriple",
				"srcs": []interface{}{"a.c"},
			},
		}
		if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
			t.Fatalf("Generate failed: %v", err)
		}
		return buf.String()
	}

	// Plain clang in target mode: -target triple must be injected so arm64
	// servers / Termux cross-target Android instead of the build host ABI.
	out := genOutput(func(o *Options) {})
	if !strings.Contains(out, "--target=aarch64-linux-android") {
		t.Errorf("Expected -target injection for plain clang in target mode")
	}

	// Explicit sysroot: --sysroot must accompany the triple.
	out = genOutput(func(o *Options) { o.SysrootDir = "/opt/android-sysroot" })
	if !strings.Contains(out, "--sysroot=/opt/android-sysroot") {
		t.Errorf("Expected --sysroot injection when SysrootDir set")
	}

	// NDK wrapper compilers already encode the target: no injection.
	out = genOutput(func(o *Options) {
		o.ClangPath = "/opt/android-ndk/toolchains/llvm/prebuilt/linux-x86_64/bin/x86_64-linux-android34-clang"
		o.ClangCxxPath = "/opt/android-ndk/toolchains/llvm/prebuilt/linux-x86_64/bin/x86_64-linux-android34-clang++"
		o.SysrootDir = "/opt/android-ndk/toolchains/llvm/prebuilt/linux-x86_64/sysroot"
	})
	if strings.Contains(out, "--target=aarch64-linux-android") {
		t.Errorf("Ninja wrapper compiler must not receive redundant -target flags")
	}
	if strings.Contains(out, "--sysroot=") {
		t.Errorf("NDK wrapper must not receive redundant --sysroot flags")
	}

	// Host mode: no Android triple injection.
	out = genOutput(func(o *Options) { o.IsHost = true })
	if strings.Contains(out, "--target=aarch64-linux-android") {
		t.Errorf("Host mode must not inject Android target triple")
	}

	// Injection must reach linker flags too, and dedup with module cflags
	// carrying the same pair must not duplicate it.
	out = genOutput(func(o *Options) {})
	linkIdx := strings.Index(out, "out/lib64/libtriple.so: link_shared")
	if linkIdx < 0 {
		t.Fatalf("Expected link_shared edge")
	}
	if !strings.Contains(out[linkIdx:], "--target=aarch64-linux-android") {
		t.Errorf("Expected -target in link flags")
	}
}

func TestBpfModule(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "bpf",
		Name: "test_progs",
		Properties: map[string]interface{}{
			"name":  "test_progs",
			"srcs":  []interface{}{"progs.c"},
			"cflags": []interface{}{"-DBPF_TEST"},
		},
	}
	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	out := buf.String()

	// Compile rule targets the BPF backend; edge carries canonical flags
	if !strings.Contains(out, "command = $cc --target=bpf -MD -MF $out.d $cflags -c $in -o $out") {
		t.Errorf("Expected bpf_compile rule with --target=bpf")
	}
	compileIdx := strings.Index(out, "build out/obj/test_progs.bpf/progs.o: bpf_compile")
	if compileIdx < 0 {
		t.Fatalf("Expected bpf_compile edge")
	}
	sectionEnd := compileIdx + 600
	if sectionEnd > len(out) {
		sectionEnd = len(out)
	}
	section := out[compileIdx:sectionEnd]
	for _, flag := range []string{"-nostdlibinc", "-DBPF_TEST", "-g", "bionic/libc/kernel/uapi"} {
		if !strings.Contains(section, flag) {
			t.Errorf("Expected %q in bpf cflags", flag)
		}
	}

	// Default btf=true strips and stages under etc/bpf
	stripIdx := strings.Index(out, "build out/etc/bpf/progs.o: bpf_strip")
	if stripIdx < 0 {
		t.Fatalf("Expected bpf_strip edge to out/etc/bpf/")
	}
	if !strings.Contains(out, "--remove-section=.rel.BTF") {
		t.Errorf("Expected BTF section removal in strip rule")
	}

	// btf: false keeps unstripped objects under obj dir only
	buf.Reset()
	gen2 := New(opts, ninja.NewWriter(&buf), nil)
	mod2 := &eval.EvaluatedModule{
		Type: "bpf",
		Name: "nobtf",
		Properties: map[string]interface{}{
			"name": "nobtf",
			"srcs": []interface{}{"x.c"},
			"btf":  false,
			"sub_dir": "overlay",
		},
	}
	if err := gen2.Generate([]*eval.EvaluatedModule{mod2}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	out2 := buf.String()
	if strings.Contains(out2, ": bpf_strip ") {
		t.Errorf("btf: false must not strip")
	}
	if strings.Contains(out2, " -g") && strings.Contains(out2, "nobtf") {
		// -g only emitted with btf enabled; check the cflags line specifically
		if cIdx := strings.Index(out2, "cflags ="); cIdx >= 0 && strings.Contains(out2[cIdx:cIdx+400], " -g") {
			t.Errorf("btf: false must not add -g")
		}
	}
	if !strings.Contains(out2, "out/etc/bpf/overlay") {
		t.Errorf("Expected sub_dir staging under out/etc/bpf/overlay")
	}
}

func TestSyspropLibrary(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	sysprop := &eval.EvaluatedModule{
		Type: "sysprop_library",
		Name: "SurfaceFlingerProperties",
		Properties: map[string]interface{}{
			"name": "SurfaceFlingerProperties",
			"srcs": []interface{}{"SurfaceFlingerProperties.sysprop"},
		},
	}
	consumer := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libsurface",
		Properties: map[string]interface{}{
			"name":        "libsurface",
			"srcs":        []interface{}{"surface.cpp"},
			"shared_libs": []interface{}{"SurfaceFlingerProperties"},
		},
	}
	if err := gen.Generate([]*eval.EvaluatedModule{sysprop, consumer}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	out := buf.String()

	// Codegen via sysprop-cli with env override
	if !strings.Contains(out, "build out/gen/SurfaceFlingerProperties/SurfaceFlingerProperties.cpp out/gen/SurfaceFlingerProperties/SurfaceFlingerProperties.h: sysprop_cpp SurfaceFlingerProperties.sysprop") {
		t.Errorf("Expected sysprop_cpp codegen edge")
	}
	if !strings.Contains(out, "${SYSPROP_CLI:-sysprop-cli} --cpp") {
		t.Errorf("Expected sysprop-cli invocation with env override")
	}

	// Compiled into lib<name>.so/.a with implicit runtime deps
	if !strings.Contains(out, "out/lib64/libSurfaceFlingerProperties.so: link_shared") {
		t.Errorf("Expected libSurfaceFlingerProperties.so link edge")
	}
	if !strings.Contains(out, "-lSurfaceFlingerProperties") {
		t.Errorf("Expected consumer -lSurfaceFlingerProperties link flag")
	}
	if !strings.Contains(out, "-lbase") {
		t.Errorf("Expected implicit -lbase runtime link")
	}

	// Consumer receives the generated header dir on its include path
	ccIdx := strings.Index(out, "build out/obj/libsurface/surface.o: cxx_compile")
	if ccIdx < 0 {
		t.Fatalf("Expected consumer compile edge")
	}
	if !strings.Contains(out[ccIdx:], "-Iout/gen/SurfaceFlingerProperties") {
		t.Errorf("Expected sysprop gen dir on consumer include path")
	}
}

func TestSyspropSourcesInCcModules(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "libsysprop_user",
		Properties: map[string]interface{}{
			"name": "libsysprop_user",
			"srcs": []interface{}{"MyProps.sysprop", "impl.cpp"},
		},
	}
	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "build out/gen/libsysprop_user/MyProps.cpp out/gen/libsysprop_user/MyProps.h: sysprop_cpp MyProps.sysprop") {
		t.Errorf("Expected inline sysprop codegen edge")
	}
	if !strings.Contains(out, "build out/obj/libsysprop_user/MyProps.o: cxx_compile out/gen/libsysprop_user/MyProps.cpp") {
		t.Errorf("Expected generated source compilation")
	}
}

func prefixTestModules() []*eval.EvaluatedModule {
	libMod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libstage",
		Properties: map[string]interface{}{
			"name": "libstage",
			"srcs": []interface{}{"stage.cpp"},
		},
	}
	binMod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "stagebin",
		Properties: map[string]interface{}{
			"name":        "stagebin",
			"srcs":        []interface{}{"main.cpp"},
			"shared_libs": []interface{}{"libstage"},
		},
	}
	return []*eval.EvaluatedModule{libMod, binMod}
}

// linkEdgeSection returns the build edge starting at edgeHead plus its
// variable assignments (everything up to the next build statement).
func linkEdgeSection(content, edgeHead string) string {
	idx := strings.Index(content, edgeHead)
	if idx < 0 {
		return ""
	}
	section := content[idx:]
	if next := strings.Index(section[1:], "\nbuild "); next >= 0 {
		section = section[:next+1]
	}
	return section
}

func TestPrefixRelativeDefaultUnchanged(t *testing.T) {
	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	if err := gen.Generate(prefixTestModules()); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()
	if strings.Contains(content, "-Wl,-rpath") {
		t.Errorf("Relative mode must not emit rpath flags, got:\n%s", content)
	}
	if strings.Contains(content, ": copy_executable ") {
		t.Errorf("Relative mode must not emit staging copy_executable edges, got:\n%s", content)
	}
	if strings.Contains(content, ": copy ") {
		t.Errorf("Relative mode must not emit staging copy edges, got:\n%s", content)
	}
	if strings.Contains(content, "-L/") {
		t.Errorf("Relative mode must not emit absolute -L paths, got:\n%s", content)
	}
	if !strings.Contains(content, "build out/bin/stagebin: link_binary") {
		t.Errorf("Expected unchanged build output layout, got:\n%s", content)
	}
	if !strings.Contains(content, "build out/lib64/libstage.so: link_shared") {
		t.Errorf("Expected unchanged lib64 layout, got:\n%s", content)
	}
}

func TestPrefixLinkSearchPath(t *testing.T) {
	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.Prefix = "/system"
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	if err := gen.Generate(prefixTestModules()); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()

	// Nothing is ever staged, installed or copied under the prefix: ninja
	// keeps building and outputting everything in the build-local out/.
	if strings.Contains(content, "build /system/") {
		t.Errorf("Prefix mode must not emit build edges under the prefix, got:\n%s", content)
	}
	if strings.Contains(content, "-Wl,-rpath") {
		t.Errorf("Prefix mode must not emit rpath flags, got:\n%s", content)
	}

	// Link search order: the build-local -Lout/lib64 stays first; the prefix
	// directory is an additional search directory after it.
	section := linkEdgeSection(content, "build out/lib64/libstage.so: link_shared")
	if section == "" {
		t.Fatalf("Expected link_shared edge for out/lib64/libstage.so")
	}
	sysIdx := strings.Index(section, "-L/system/lib64")
	outIdx := strings.Index(section, "-Lout/lib64")
	if sysIdx < 0 || outIdx < 0 {
		t.Fatalf("Expected -Lout/lib64 and -L/system/lib64 on link edge, got:\n%s", section)
	}
	if sysIdx < outIdx {
		t.Errorf("Expected -Lout/lib64 before -L/system/lib64, got:\n%s", section)
	}

	// Module target lists reference only build-local out/ outputs.
	gen2 := New(opts, ninja.NewWriter(&bytes.Buffer{}), nil)
	binTargets, err := gen2.generateCcBinary(
		&eval.EvaluatedModule{
			Type: "cc_binary",
			Name: "stagebin",
			Properties: map[string]interface{}{
				"name":        "stagebin",
				"srcs":        []interface{}{"main.cpp"},
				"shared_libs": []interface{}{"libstage"},
			},
		})
	if err != nil {
		t.Fatalf("generateCcBinary failed: %v", err)
	}
	libTargets, err := gen2.generateCcLibrary(
		&eval.EvaluatedModule{
			Type: "cc_library",
			Name: "libstage",
			Properties: map[string]interface{}{
				"name": "libstage",
				"srcs": []interface{}{"stage.cpp"},
			},
		})
	if err != nil {
		t.Fatalf("generateCcLibrary failed: %v", err)
	}
	for _, tg := range append(append([]string{}, binTargets...), libTargets...) {
		if filepath.IsAbs(tg) || !strings.HasPrefix(tg, "out"+string(filepath.Separator)) {
			t.Errorf("Prefix mode target lists must contain only out/ paths, got %q", tg)
		}
	}

	// Default targets keep referencing the build outputs only.
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "default ") && strings.Contains(line, "/system/") {
			t.Errorf("Default targets must not reference prefix paths: %s", line)
		}
	}
}

func TestPrefixX32(t *testing.T) {
	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.TargetArch = "x32"
	opts.Prefix = "/apex/com.android.art"
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libx32test",
		Properties: map[string]interface{}{
			"name": "libx32test",
			"srcs": []interface{}{"a.c"},
		},
	}
	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	content := buf.String()

	// No staging edges into the APEX prefix; no runtime rpath flags.
	if strings.Contains(content, "build /apex/") {
		t.Errorf("Prefix mode must not emit build edges under the prefix, got:\n%s", content)
	}
	if strings.Contains(content, "-Wl,-rpath") {
		t.Errorf("Prefix mode must not emit rpath flags, got:\n%s", content)
	}

	// -Lout/libx32 comes first; -L/apex/com.android.art/libx32 is the
	// additional search directory emitted after it.
	section := linkEdgeSection(content, "build out/libx32/libx32test.so: link_shared")
	if section == "" {
		t.Fatalf("Expected link_shared edge for out/libx32/libx32test.so")
	}
	pxIdx := strings.Index(section, "-L/apex/com.android.art/libx32")
	outIdx := strings.Index(section, "-Lout/libx32")
	if pxIdx < 0 || outIdx < 0 {
		t.Fatalf("Expected -Lout/libx32 and -L/apex/com.android.art/libx32 on link edge, got:\n%s", section)
	}
	if pxIdx < outIdx {
		t.Errorf("Expected -Lout/libx32 before -L/apex/com.android.art/libx32, got:\n%s", section)
	}
}

func TestPrefixRelativeDotAndSlash(t *testing.T) {
	newContent := func(prefix string) string {
		var buf strings.Builder
		nw := ninja.NewWriter(&buf)
		opts := DefaultOptions(".", "out")
		opts.Prefix = prefix
		opts.AllowMissingDeps = true
		gen := New(opts, nw, nil)
		if err := gen.Generate(prefixTestModules()); err != nil {
			t.Fatalf("Generate failed for prefix %q: %v", prefix, err)
		}
		return buf.String()
	}

	for _, prefix := range []string{".", "/"} {
		content := newContent(prefix)
		if strings.Contains(content, "-Wl,-rpath") {
			t.Errorf("prefix %q: relative mode must not emit rpath flags, got:\n%s", prefix, content)
		}
		if strings.Contains(content, ": copy_executable ") {
			t.Errorf("prefix %q: relative mode must not emit staging copy_executable edges, got:\n%s", prefix, content)
		}
		if strings.Contains(content, ": copy ") {
			t.Errorf("prefix %q: relative mode must not emit staging copy edges, got:\n%s", prefix, content)
		}
		if strings.Contains(content, "-L/") {
			t.Errorf("prefix %q: relative mode must not emit absolute -L paths, got:\n%s", prefix, content)
		}
	}
}

// TestPrefixNinjaDryRun feeds the generated manifest to a real ninja dry run:
// the run succeeds and the expanded link command carries the prefix library
// search directory (-L/system/lib64) without any $ORIGIN runtime flags.
func TestPrefixNinjaDryRun(t *testing.T) {
	ninjaBin, err := exec.LookPath("ninja")
	if err != nil {
		t.Skip("ninja not available for dry-run check")
	}
	tmp := t.TempDir()
	var buf strings.Builder
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.Prefix = "/system"
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	depMod := &eval.EvaluatedModule{
		Type: "cc_library_static",
		Name: "libdep",
		Properties: map[string]interface{}{
			"name": "libdep",
			"srcs": []interface{}{"dep.c"},
		},
	}
	binMod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "drybin",
		Properties: map[string]interface{}{
			"name":        "drybin",
			"srcs":        []interface{}{"main.c"},
			"static_libs": []interface{}{"libdep"},
		},
	}
	if err := gen.Generate([]*eval.EvaluatedModule{depMod, binMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	buildFile := filepath.Join(tmp, "build.ninja")
	if err := os.WriteFile(buildFile, []byte(buf.String()), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	// ninja (>=1.11) errors on missing inputs even with -n, so provide them.
	for _, src := range []string{"dep.c", "main.c"} {
		if err := os.WriteFile(filepath.Join(tmp, src), []byte("int impl(void) { return 0; }\n"), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
	}

	// -v: print expanded commands (plain -n only prints descriptions).
	cmd := exec.Command(ninjaBin, "-f", buildFile, "-n", "-v")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.Dir = tmp
	if err := cmd.Run(); err != nil {
		t.Fatalf("ninja -n dry run failed: %v\n%s", err, out.String())
	}

	var linkCmd string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "-o out/bin/drybin") {
			linkCmd = line
			break
		}
	}
	if linkCmd == "" {
		t.Fatalf("Expected link command for out/bin/drybin in dry-run output, got:\n%s", out.String())
	}
	if !strings.Contains(linkCmd, "-L/system/lib64") {
		t.Errorf("Expected -L/system/lib64 in expanded link command, got:\n%s", linkCmd)
	}
	if strings.Contains(out.String(), "$ORIGIN") {
		t.Errorf("Expanded dry-run output must not contain $ORIGIN, got:\n%s", out.String())
	}
}
