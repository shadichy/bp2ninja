package generator

import (
	"bytes"
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

