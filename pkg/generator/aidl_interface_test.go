package generator

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

func TestAidlInterfaceCppAndNdk(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "out")

	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	aidlMod := &eval.EvaluatedModule{
		Type: "aidl_interface",
		Name: "android.hardware.power",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":      "android.hardware.power",
			"srcs":      []interface{}{"IPower.aidl"},
			"stability": "vintf",
			"backend": map[string]interface{}{
				"cpp": map[string]interface{}{
					"enabled": true,
				},
				"ndk": map[string]interface{}{
					"enabled": true,
				},
				"java": map[string]interface{}{
					"enabled": false,
				},
				"rust": map[string]interface{}{
					"enabled": true,
				},
			},
			"versions": []interface{}{"1", "2"},
		},
	}

	ccMod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "power_client",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":        "power_client",
			"srcs":        []interface{}{"client.cpp"},
			"static_libs": []interface{}{"android.hardware.power-cpp"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{aidlMod, ccMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// 1. Verify C++ backend outputs and rules
	cppStatic := filepath.Join(gen.libDir(), "libandroid.hardware.power-cpp.a")
	cppShared := filepath.Join(gen.libDir(), "libandroid.hardware.power-cpp.so")
	if !strings.Contains(ninjaContent, cppStatic) {
		t.Errorf("Expected C++ static library %s in ninja:\n%s", cppStatic, ninjaContent)
	}
	if !strings.Contains(ninjaContent, cppShared) {
		t.Errorf("Expected C++ shared library %s in ninja:\n%s", cppShared, ninjaContent)
	}
	if !strings.Contains(ninjaContent, "rule aidl_cpp") || !strings.Contains(ninjaContent, ": aidl_cpp ") {
		t.Errorf("Expected rule aidl_cpp in ninja:\n%s", ninjaContent)
	}

	// 2. Verify NDK backend outputs and rules
	ndkStatic := filepath.Join(gen.libDir(), "libandroid.hardware.power-ndk.a")
	ndkShared := filepath.Join(gen.libDir(), "libandroid.hardware.power-ndk.so")
	if !strings.Contains(ninjaContent, ndkStatic) {
		t.Errorf("Expected NDK static library %s in ninja:\n%s", ndkStatic, ninjaContent)
	}
	if !strings.Contains(ninjaContent, ndkShared) {
		t.Errorf("Expected NDK shared library %s in ninja:\n%s", ndkShared, ninjaContent)
	}
	if !strings.Contains(ninjaContent, "rule aidl_ndk") || !strings.Contains(ninjaContent, ": aidl_ndk ") {
		t.Errorf("Expected rule aidl_ndk in ninja:\n%s", ninjaContent)
	}

	// 3. Verify Rust backend output
	rustOut := filepath.Join(outDir, "gen", "android.hardware.power", "rust", "android.hardware.power.rs")
	if !strings.Contains(ninjaContent, rustOut) {
		t.Errorf("Expected Rust binding %s in ninja:\n%s", rustOut, ninjaContent)
	}
	if !strings.Contains(ninjaContent, "rule aidl_rust") || !strings.Contains(ninjaContent, ": aidl_rust ") {
		t.Errorf("Expected rule aidl_rust in ninja:\n%s", ninjaContent)
	}

	// 4. Verify version aliases
	if !strings.Contains(ninjaContent, "build android.hardware.power-V1-cpp: phony") {
		t.Errorf("Expected android.hardware.power-V1-cpp phony in ninja:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "build android.hardware.power-V2-ndk: phony") {
		t.Errorf("Expected android.hardware.power-V2-ndk phony in ninja:\n%s", ninjaContent)
	}

	// 5. Verify cc_library links -l:libandroid.hardware.power-cpp.a
	if !strings.Contains(ninjaContent, "-l:libandroid.hardware.power-cpp.a") {
		t.Errorf("Expected cc_library to link -l:libandroid.hardware.power-cpp.a:\n%s", ninjaContent)
	}

	// 6. Verify include directory propagation
	expectedInc := "-I" + filepath.Join(outDir, "gen", "android.hardware.power", "cpp", "include")
	if !strings.Contains(ninjaContent, expectedInc) {
		t.Errorf("Expected cc_library to receive include path %s:\n%s", expectedInc, ninjaContent)
	}
}

func TestAidlInterfaceJavaBackend(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "out")

	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	aidlMod := &eval.EvaluatedModule{
		Type: "aidl_interface",
		Name: "android.hardware.health",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name": "android.hardware.health",
			"srcs": []interface{}{"IHealth.aidl"},
			"backend": map[string]interface{}{
				"cpp": map[string]interface{}{
					"enabled": false,
				},
				"ndk": map[string]interface{}{
					"enabled": false,
				},
				"java": map[string]interface{}{
					"enabled": true,
				},
			},
		},
	}

	javaMod := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "health_service",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":        "health_service",
			"srcs":        []interface{}{"HealthService.java"},
			"static_libs": []interface{}{"android.hardware.health-java"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{aidlMod, javaMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// Verify aidl_java rule and jar target
	expectedJar := filepath.Join(gen.libDir(), "android.hardware.health-java.jar")
	if !strings.Contains(ninjaContent, expectedJar) {
		t.Errorf("Expected %s in ninja:\n%s", expectedJar, ninjaContent)
	}
	if !strings.Contains(ninjaContent, "rule aidl_java") || !strings.Contains(ninjaContent, ": aidl_java ") {
		t.Errorf("Expected rule aidl_java in ninja:\n%s", ninjaContent)
	}

	// Verify java_library receives the aidl jar on classpath
	if !strings.Contains(ninjaContent, expectedJar) {
		t.Errorf("Expected java_library to depend on %s", expectedJar)
	}
}

func TestDroidstubsMetalava(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "out")

	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	stubsMod := &eval.EvaluatedModule{
		Type: "droidstubs",
		Name: "api-stubs-docs-non-updatable",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name": "api-stubs-docs-non-updatable",
			"srcs": []interface{}{"Framework.java"},
			"stub_packages": []interface{}{
				"android",
				"com.android",
			},
			"api_filename": "public_api.txt",
			"args":         "--hide 111",
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{stubsMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	stubsJar := filepath.Join(outDir, "gen", "api-stubs-docs-non-updatable", "stubs.srcjar")
	apiTxt := filepath.Join(outDir, "gen", "api-stubs-docs-non-updatable", "public_api.txt")

	if !strings.Contains(ninjaContent, stubsJar) {
		t.Errorf("Expected stubs.srcjar %s in ninja:\n%s", stubsJar, ninjaContent)
	}
	if !strings.Contains(ninjaContent, apiTxt) {
		t.Errorf("Expected public_api.txt %s in ninja:\n%s", apiTxt, ninjaContent)
	}
	if !strings.Contains(ninjaContent, "rule metalava_stubs") || !strings.Contains(ninjaContent, ": metalava_stubs ") {
		t.Errorf("Expected rule metalava_stubs in ninja:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "--stub-packages android:com.android") {
		t.Errorf("Expected --stub-packages in ninja command:\n%s", ninjaContent)
	}
}

func TestAidlInterfaceImportLocalIncludeDir(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "out")

	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	importedMod := &eval.EvaluatedModule{
		Type: "aidl_interface",
		Name: "android.media.soundtrigger.types",
		Dir:  "system/hardware/interfaces/media",
		Properties: map[string]interface{}{
			"name":              "android.media.soundtrigger.types",
			"local_include_dir": "aidl",
			"srcs":              []interface{}{"aidl/android/media/soundtrigger/PhraseRecognitionExtra.aidl"},
		},
	}

	consumerMod := &eval.EvaluatedModule{
		Type: "aidl_interface",
		Name: "soundtrigger_middleware-aidl",
		Dir:  "frameworks/base/media/soundtrigger",
		Properties: map[string]interface{}{
			"name":    "soundtrigger_middleware-aidl",
			"imports": []interface{}{"android.media.soundtrigger.types-V1"},
			"srcs":    []interface{}{"ISoundTriggerMiddlewareService.aidl"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{importedMod, consumerMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()
	expectedInc := "-Isystem/hardware/interfaces/media/aidl"
	if !strings.Contains(ninjaContent, expectedInc) {
		t.Errorf("Expected %s in ninja imports, got:\n%s", expectedInc, ninjaContent)
	}
}
