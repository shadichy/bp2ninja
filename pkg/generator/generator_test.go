package generator

import (
	"bytes"
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
