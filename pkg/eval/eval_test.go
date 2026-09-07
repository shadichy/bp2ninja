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
