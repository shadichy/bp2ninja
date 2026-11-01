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

func TestRustFfiStaticAndCargoToml(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "out")

	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.Cargo = true
	opts.CargoOffline = true
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	srcFile := filepath.Join(tempDir, "src", "lib.rs")
	if err := os.MkdirAll(filepath.Dir(srcFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcFile, []byte("pub extern \"C\" fn foo() {}"), 0644); err != nil {
		t.Fatal(err)
	}

	rustMod := &eval.EvaluatedModule{
		Type: "rust_ffi_static",
		Name: "librustc_demangle_static",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":              "librustc_demangle_static",
			"crate_name":        "rustc_demangle",
			"srcs":              []interface{}{srcFile},
			"edition":           "2018",
			"cargo_pkg_version": "0.1.21",
			"features":          []interface{}{"default"},
			"export_include_dirs": []interface{}{
				"include",
			},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{rustMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// Verify cargo_build rule invocation
	if !strings.Contains(ninjaContent, "build "+filepath.Join(gen.libDir(), "librustc_demangle_static.a")) {
		t.Errorf("Expected out/lib64/librustc_demangle_static.a in ninja outputs:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "rule cargo_build") || !strings.Contains(ninjaContent, ": cargo_build ") {
		t.Errorf("Expected cargo_build rule in ninja file:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "--offline") {
		t.Errorf("Expected --offline flag passed to cargo in ninja file:\n%s", ninjaContent)
	}

	// Verify synthesized Cargo.toml
	cargoTomlPath := filepath.Join(outDir, "cargo", "librustc_demangle_static", "Cargo.toml")
	data, err := os.ReadFile(cargoTomlPath)
	if err != nil {
		t.Fatalf("Failed to read synthesized Cargo.toml at %s: %v", cargoTomlPath, err)
	}
	content := string(data)
	if !strings.Contains(content, `name = "rustc_demangle"`) {
		t.Errorf("Cargo.toml missing crate name:\n%s", content)
	}
	if !strings.Contains(content, `crate-type = ["staticlib"]`) {
		t.Errorf("Cargo.toml missing staticlib crate-type:\n%s", content)
	}
	if !strings.Contains(content, `edition = "2018"`) {
		t.Errorf("Cargo.toml missing edition 2018:\n%s", content)
	}
	if !strings.Contains(content, `version = "0.1.21"`) {
		t.Errorf("Cargo.toml missing version 0.1.21:\n%s", content)
	}
}

func TestCcLibraryLinksRustFfi(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "out")

	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.Cargo = true
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	srcRs := filepath.Join(tempDir, "src", "lib.rs")
	if err := os.MkdirAll(filepath.Dir(srcRs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcRs, []byte("pub extern \"C\" fn input_parse() {}"), 0644); err != nil {
		t.Fatal(err)
	}

	rustMod := &eval.EvaluatedModule{
		Type: "rust_ffi_static",
		Name: "libinput_rust_ffi",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":                "libinput_rust_ffi",
			"crate_name":          "input_rust_ffi",
			"srcs":                []interface{}{srcRs},
			"export_include_dirs": []interface{}{"include"},
		},
	}

	ccMod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libinput",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":        "libinput",
			"srcs":        []interface{}{"Input.cpp"},
			"static_libs": []interface{}{"libinput_rust_ffi"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{rustMod, ccMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// Verify cc_library links -l:libinput_rust_ffi.a
	if !strings.Contains(ninjaContent, "-l:libinput_rust_ffi.a") {
		t.Errorf("Expected cc_library to link -l:libinput_rust_ffi.a:\n%s", ninjaContent)
	}

	// Verify cc_library compiles with include path from rust_ffi export_include_dirs
	incExpected := "-I" + filepath.Join(tempDir, "include")
	if !strings.Contains(ninjaContent, incExpected) {
		t.Errorf("Expected cc_library to include %s from rust_ffi:\n%s", incExpected, ninjaContent)
	}
}

func TestRustBinaryAndProcMacro(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "out")

	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.Cargo = true
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	mainRs := filepath.Join(tempDir, "src", "main.rs")
	macroRs := filepath.Join(tempDir, "macro", "lib.rs")
	if err := os.MkdirAll(filepath.Dir(mainRs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(macroRs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainRs, []byte("fn main() {}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(macroRs, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	macroMod := &eval.EvaluatedModule{
		Type: "rust_proc_macro",
		Name: "libmy_macro",
		Dir:  filepath.Join(tempDir, "macro"),
		Properties: map[string]interface{}{
			"name":       "libmy_macro",
			"crate_name": "my_macro",
			"srcs":       []interface{}{macroRs},
		},
	}

	binMod := &eval.EvaluatedModule{
		Type: "rust_binary",
		Name: "my_rust_tool",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":        "my_rust_tool",
			"srcs":        []interface{}{mainRs},
			"proc_macros": []interface{}{"libmy_macro"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{macroMod, binMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// Verify proc macro cargo manifest
	macroTomlPath := filepath.Join(outDir, "cargo", "libmy_macro", "Cargo.toml")
	data, err := os.ReadFile(macroTomlPath)
	if err != nil {
		t.Fatalf("Failed to read macro Cargo.toml: %v", err)
	}
	if !strings.Contains(string(data), "proc-macro = true") {
		t.Errorf("Expected proc-macro = true in %s", string(data))
	}

	// Verify binary cargo manifest
	binTomlPath := filepath.Join(outDir, "cargo", "my_rust_tool", "Cargo.toml")
	binData, err := os.ReadFile(binTomlPath)
	if err != nil {
		t.Fatalf("Failed to read bin Cargo.toml: %v", err)
	}
	if !strings.Contains(string(binData), "[[bin]]") {
		t.Errorf("Expected [[bin]] in %s", string(binData))
	}

	// Verify binary output in out/bin
	expectedBin := filepath.Join(gen.binDir(), "my_rust_tool")
	if !strings.Contains(ninjaContent, expectedBin) {
		t.Errorf("Expected %s in ninja output:\n%s", expectedBin, ninjaContent)
	}
}

func TestRustBindgen(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "out")

	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	hdrPath := filepath.Join(tempDir, "include", "foo.h")
	if err := os.MkdirAll(filepath.Dir(hdrPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hdrPath, []byte("int foo(void);"), 0644); err != nil {
		t.Fatal(err)
	}

	bindgenMod := &eval.EvaluatedModule{
		Type: "rust_bindgen",
		Name: "libfoo_bindgen",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":        "libfoo_bindgen",
			"wrapper_src": "include/foo.h",
			"source_stem": "bindings",
			"cflags":      []interface{}{"-DFOO_FLAG"},
			"bindgen_flags": []interface{}{
				"--allowlist-function", "foo",
			},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{bindgenMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	expectedOut := filepath.Join(outDir, "gen", "libfoo_bindgen", "bindings.rs")
	if !strings.Contains(ninjaContent, expectedOut) {
		t.Errorf("Expected %s in ninja outputs:\n%s", expectedOut, ninjaContent)
	}
	if !strings.Contains(ninjaContent, "rule rust_bindgen") || !strings.Contains(ninjaContent, ": rust_bindgen ") {
		t.Errorf("Expected rule rust_bindgen in ninja file:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "--allowlist-function foo") {
		t.Errorf("Expected bindgen flags in ninja file:\n%s", ninjaContent)
	}
}
