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

func TestBuildClasspathPhysicalJarPrioritization(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bp2ninja_java_prebuilts_*")
	if err != nil {
		t.Fatalf("Failed to create tempDir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create physical prebuilt shadow structure: prebuilts/target/art/share/art/art-module-lib.jar
	artDir := filepath.Join(tempDir, "prebuilts/target/art/share/art")
	if err := os.MkdirAll(artDir, 0755); err != nil {
		t.Fatalf("Failed to mkdir: %v", err)
	}
	physJarPath := filepath.Join(artDir, "art-module-lib.jar")
	if err := os.WriteFile(physJarPath, []byte("PK_MOCK_JAR_HEADER"), 0644); err != nil {
		t.Fatalf("Failed to write mock jar: %v", err)
	}

	outDir := filepath.Join(tempDir, "out")
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	// Java library depending on conceptual Soong target: art.module.public.api.stubs.module_lib
	javaLib := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "my-service",
		Dir:  "frameworks/services",
		Properties: map[string]interface{}{
			"name": "my-service",
			"libs": []interface{}{
				"art.module.public.api.stubs.module_lib",
			},
			"srcs": []interface{}{
				"Service.java",
			},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{javaLib}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// 1. Verify that the physical jar path is included in javac -cp
	if !strings.Contains(ninjaContent, physJarPath) {
		t.Errorf("Expected physical jar %s in javac classpath, got:\n%s", physJarPath, ninjaContent)
	}

	// 2. Verify that NO phony rule is generated for out/lib64/art.module.public.api.stubs.module_lib.jar
	phonyName := filepath.Join(outDir, "lib64/art.module.public.api.stubs.module_lib.jar")
	if strings.Contains(ninjaContent, "build "+phonyName+": phony") {
		t.Errorf("Expected NO phony rule for %s when physical jar exists, but found one in:\n%s", phonyName, ninjaContent)
	}
}

func TestExplicitBootclasspathAndPrebuiltJars(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bp2ninja_bcp_test_*")
	if err != nil {
		t.Fatalf("Failed to create tempDir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mockBcp := filepath.Join(tempDir, "custom-boot.jar")
	mockExtra := filepath.Join(tempDir, "custom-extra.jar")
	_ = os.WriteFile(mockBcp, []byte("BCP"), 0644)
	_ = os.WriteFile(mockExtra, []byte("EXTRA"), 0644)

	outDir := filepath.Join(tempDir, "out")
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	opts.BootClasspath = []string{mockBcp}
	opts.ExtraJars = []string{mockExtra}
	gen := New(opts, nw, nil)

	javaLib := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "test-app",
		Dir:  "apps/test",
		Properties: map[string]interface{}{
			"name": "test-app",
			"srcs": []interface{}{
				"App.java",
			},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{javaLib}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// Verify bootclasspath and extra jar are placed first
	expectedCpPrefix := "-cp " + mockBcp + ":" + mockExtra
	if !strings.Contains(ninjaContent, expectedCpPrefix) {
		t.Errorf("Expected classpath to start with %s, got:\n%s", expectedCpPrefix, ninjaContent)
	}
}

func TestSoongPrebuiltJarAliases(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bp2ninja_aliases_test_*")
	if err != nil {
		t.Fatalf("Failed to create tempDir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	prebuiltDir := filepath.Join(tempDir, "prebuilts")
	_ = os.MkdirAll(prebuiltDir, 0755)

	coreJar := filepath.Join(prebuiltDir, "core-libart.jar")
	connJar := filepath.Join(prebuiltDir, "framework-connectivity.jar")
	conscryptJar := filepath.Join(prebuiltDir, "conscrypt-module-lib.jar")
	_ = os.WriteFile(coreJar, []byte("JAR"), 0644)
	_ = os.WriteFile(connJar, []byte("JAR"), 0644)
	_ = os.WriteFile(conscryptJar, []byte("JAR"), 0644)

	opts := DefaultOptions(tempDir, filepath.Join(tempDir, "out"))
	opts.PrebuiltJarDirs = []string{prebuiltDir}
	gen := New(opts, ninja.NewWriter(&bytes.Buffer{}), nil)

	// Test alias resolution
	if resolved := gen.resolvePhysicalJar("core.platform.api.stubs"); resolved != coreJar {
		t.Errorf("Expected core.platform.api.stubs to resolve to %s, got %s", coreJar, resolved)
	}
	if resolved := gen.resolvePhysicalJar("framework-connectivity"); resolved != connJar {
		t.Errorf("Expected framework-connectivity to resolve to %s, got %s", connJar, resolved)
	}
	if resolved := gen.resolvePhysicalJar("conscrypt.module.platform.api.stubs"); resolved != conscryptJar {
		t.Errorf("Expected conscrypt.module.platform.api.stubs to resolve to %s, got %s", conscryptJar, resolved)
	}
}

func TestDroidstubsClasspathPhysicalJars(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bp2ninja_droidstubs_cp_*")
	if err != nil {
		t.Fatalf("Failed to create tempDir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	prebuiltDir := filepath.Join(tempDir, "prebuilt_jars")
	_ = os.MkdirAll(prebuiltDir, 0755)
	artJar := filepath.Join(prebuiltDir, "art-module-lib.jar")
	_ = os.WriteFile(artJar, []byte("ART"), 0644)

	outDir := filepath.Join(tempDir, "out")
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.PrebuiltJarDirs = []string{prebuiltDir}
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	droidstubsMod := &eval.EvaluatedModule{
		Type: "droidstubs",
		Name: "api-stubs-docs",
		Dir:  "frameworks/base",
		Properties: map[string]interface{}{
			"name": "api-stubs-docs",
			"srcs": []interface{}{
				"Stub.java",
			},
			"libs": []interface{}{
				"art.module.public.api.stubs.module_lib",
			},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{droidstubsMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// Verify metalava received physical jar in --classpath
	expectedArg := "--classpath " + artJar
	if !strings.Contains(ninjaContent, expectedArg) {
		t.Errorf("Expected metalava command to contain %s, got:\n%s", expectedArg, ninjaContent)
	}
}
