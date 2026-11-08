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

func TestMavenRepositoryJarIndexingAndCopy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bp2ninja_m2_test_*")
	if err != nil {
		t.Fatalf("Failed to create tempDir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create m2repository structure:
	// prebuilts/sdk/current/androidx/m2repository/androidx/annotation/annotation-jvm/1.8.0-alpha01/annotation-jvm-1.8.0-alpha01.jar
	m2Dir := filepath.Join(tempDir, "prebuilts/sdk/current/androidx/m2repository/androidx/annotation/annotation-jvm/1.8.0-alpha01")
	if err := os.MkdirAll(m2Dir, 0755); err != nil {
		t.Fatalf("Failed to mkdir: %v", err)
	}
	annotationJar := filepath.Join(m2Dir, "annotation-jvm-1.8.0-alpha01.jar")
	if err := os.WriteFile(annotationJar, []byte("ANNOTATION_JAR"), 0644); err != nil {
		t.Fatalf("Failed to write mock jar: %v", err)
	}
	sourcesJar := filepath.Join(m2Dir, "annotation-jvm-1.8.0-alpha01-sources.jar")
	if err := os.WriteFile(sourcesJar, []byte("SOURCES_JAR"), 0644); err != nil {
		t.Fatalf("Failed to write mock sources jar: %v", err)
	}

	outDir := filepath.Join(tempDir, "out")
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	// 1. Verify resolution directly
	resolved := gen.resolvePhysicalJar("androidx.annotation_annotation")
	if resolved != annotationJar {
		t.Errorf("Expected androidx.annotation_annotation to resolve to %s, got %s", annotationJar, resolved)
	}

	// 2. java_library with no srcs (composite / static lib) representing androidx.annotation_annotation
	annotationLib := &eval.EvaluatedModule{
		Type: "java_library_static",
		Name: "androidx.annotation_annotation",
		Dir:  "prebuilts/sdk/current/androidx/m2repository/androidx/annotation/annotation-jvm/1.8.0-alpha01",
		Properties: map[string]interface{}{
			"name": "androidx.annotation_annotation",
		},
	}

	// Consumer java_library depending on androidx.annotation_annotation via libs
	consumerLib := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "modules-utils-build",
		Dir:  "frameworks/libs/modules-utils/build",
		Properties: map[string]interface{}{
			"name": "modules-utils-build",
			"libs": []interface{}{
				"androidx.annotation_annotation",
			},
			"srcs": []interface{}{
				"SdkLevel.java",
			},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{annotationLib, consumerLib}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// Verify that the consumer classpath has the physical annotation jar, NOT a nonexistent phony
	if !strings.Contains(ninjaContent, annotationJar) {
		t.Errorf("Expected consumer classpath to contain physical jar %s, got:\n%s", annotationJar, ninjaContent)
	}
	// Verify that the annotation lib has a copy rule, not a phony rule
	expectedCopy := "build " + filepath.Join(outDir, "lib64/androidx.annotation_annotation.jar: copy " + annotationJar)
	if !strings.Contains(ninjaContent, expectedCopy) {
		t.Errorf("Expected copy rule %q in ninja file, got:\n%s", expectedCopy, ninjaContent)
	}
}

func TestAndroidAppAaptSrcjar(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bp2ninja_aapt_srcjar_*")
	if err != nil {
		t.Fatalf("Failed to create tempDir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	manifestPath := filepath.Join(tempDir, "AndroidManifest.xml")
	_ = os.WriteFile(manifestPath, []byte("<manifest/>"), 0644)
	resDir := filepath.Join(tempDir, "res")
	_ = os.MkdirAll(resDir, 0755)

	outDir := filepath.Join(tempDir, "out")
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	platformResApp := &eval.EvaluatedModule{
		Type: "android_app",
		Name: "org.lineageos.platform-res",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":     "org.lineageos.platform-res",
			"manifest": manifestPath,
			"aaptflags": []interface{}{
				"--private-symbols",
				"org.lineageos.platform.internal",
				"--no-auto-version",
			},
			"resource_dirs": []interface{}{
				resDir,
			},
		},
	}

	consumerLib := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "org.lineageos.platform.internal",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name": "org.lineageos.platform.internal",
			"srcs": []interface{}{
				"NetworkTraffic.java",
				":org.lineageos.platform-res{.aapt.srcjar}",
			},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{platformResApp, consumerLib}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()
	srcjarTarget := filepath.Join(outDir, "gen/org.lineageos.platform-res/org.lineageos.platform-res.srcjar")

	// 1. AAPT2 link outputs srcjarTarget and uses --java and --private-symbols
	if !strings.Contains(ninjaContent, srcjarTarget) {
		t.Errorf("Expected ninja output to contain %s, got:\n%s", srcjarTarget, ninjaContent)
	}
	if !strings.Contains(ninjaContent, "--private-symbols org.lineageos.platform.internal") {
		t.Errorf("Expected aapt2 command to contain --private-symbols, got:\n%s", ninjaContent)
	}

	// 2. Downstream consumer must include srcjarTarget in its javac edge inputs & unzipping
	if !strings.Contains(ninjaContent, srcjarTarget) {
		t.Errorf("Expected consumer to depend on %s", srcjarTarget)
	}
	if !strings.Contains(ninjaContent, "for sj in "+srcjarTarget) {
		t.Errorf("Expected consumer javac rule to extract %s", srcjarTarget)
	}
}

func TestJavaSdkLibraryInTreeImplementation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bp2ninja_sdk_lib_*")
	if err != nil {
		t.Fatalf("Failed to create tempDir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	outDir := filepath.Join(tempDir, "out")
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	fg := &eval.EvaluatedModule{
		Type: "filegroup",
		Name: "framework-nfc-updatable-sources",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name": "framework-nfc-updatable-sources",
			"srcs": []interface{}{
				"NfcAdapter.java",
			},
		},
	}

	nfcSdkLib := &eval.EvaluatedModule{
		Type: "java_sdk_library",
		Name: "framework-nfc",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name": "framework-nfc",
			"srcs": []interface{}{
				":framework-nfc-updatable-sources",
			},
		},
	}

	consumerLib := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "nfc-extras",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name": "nfc-extras",
			"libs": []interface{}{
				"framework-nfc.impl",
			},
			"srcs": []interface{}{
				"NfcExtras.java",
			},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{fg, nfcSdkLib, consumerLib}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()
	nfcJar := filepath.Join(outDir, "lib64/framework-nfc.jar")
	nfcImplJar := filepath.Join(outDir, "lib64/framework-nfc.impl.jar")

	// 1. Verify in-tree framework-nfc compiles to nfcJar
	if !strings.Contains(ninjaContent, "build "+nfcJar) {
		t.Errorf("Expected rule building %s in ninja file", nfcJar)
	}

	// 2. Verify framework-nfc.impl.jar copy rule
	expectedCopy := "build " + nfcImplJar + ": copy " + nfcJar
	if !strings.Contains(ninjaContent, expectedCopy) {
		t.Errorf("Expected copy rule %q in ninja file, got:\n%s", expectedCopy, ninjaContent)
	}

	// 3. Verify consumer uses local nfcImplJar in classpath
	if !strings.Contains(ninjaContent, nfcImplJar) {
		t.Errorf("Expected consumer to include local %s on classpath", nfcImplJar)
	}
}

func TestSdkStubScopeIndexingAndDummyJarProtection(t *testing.T) {
	tempDir := t.TempDir()
	outDir := filepath.Join(tempDir, "out")

	// 1. Create dummy public jar (< 500 bytes)
	publicDir := filepath.Join(tempDir, "prebuilts/sdk/34/public")
	if err := os.MkdirAll(publicDir, 0755); err != nil {
		t.Fatalf("Failed to mkdir: %v", err)
	}
	dummyJarPath := filepath.Join(publicDir, "service-rkp.jar")
	if err := os.WriteFile(dummyJarPath, []byte("PK\x05\x06dummy"), 0644); err != nil {
		t.Fatalf("Failed to write dummy jar: %v", err)
	}

	// 2. Create real system-server jar (>= 500 bytes)
	sysServerDir := filepath.Join(tempDir, "prebuilts/sdk/34/system-server")
	if err := os.MkdirAll(sysServerDir, 0755); err != nil {
		t.Fatalf("Failed to mkdir: %v", err)
	}
	realJarPath := filepath.Join(sysServerDir, "service-rkp.jar")
	realContent := make([]byte, 1024)
	copy(realContent, "PK\x03\x04realjarcontent")
	if err := os.WriteFile(realJarPath, realContent, 0644); err != nil {
		t.Fatalf("Failed to write real jar: %v", err)
	}

	// 3. Create dummy art-module-lib for patch-module verification
	artDir := filepath.Join(tempDir, "prebuilts/target/art/share/art")
	if err := os.MkdirAll(artDir, 0755); err != nil {
		t.Fatalf("Failed to mkdir: %v", err)
	}
	artJarPath := filepath.Join(artDir, "art-module-lib.jar")
	artContent := make([]byte, 1024)
	if err := os.WriteFile(artJarPath, artContent, 0644); err != nil {
		t.Fatalf("Failed to write art jar: %v", err)
	}

	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.APILevel = 34
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	gen.initPrebuiltJarIndex()

	// Verify scope-specific indexing
	resolvedScope := gen.resolvePhysicalJar("service-rkp.stubs.system_server")
	if resolvedScope != realJarPath {
		t.Errorf("Expected service-rkp.stubs.system_server to resolve to %s, got %s", realJarPath, resolvedScope)
	}

	// Verify dummy jar protection replaced dummy with real jar
	resolvedBase := gen.resolvePhysicalJar("service-rkp")
	if resolvedBase != realJarPath {
		t.Errorf("Expected base service-rkp to resolve to non-dummy %s, got %s", realJarPath, resolvedBase)
	}

	// 4. Verify patch-module injection for platform Java module (empty sdk_version)
	mod := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "services.core",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name": "services.core",
			"srcs": []interface{}{
				"Service.java",
			},
		},
	}
	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()
	if !strings.Contains(ninjaContent, "--patch-module java.base="+artJarPath) {
		t.Errorf("Expected ninja to pass --patch-module java.base with art-module-lib, got:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "--add-exports=java.base/java.nio=ALL-UNNAMED") {
		t.Errorf("Expected ninja to pass --add-exports for java.nio, got:\n%s", ninjaContent)
	}
}

func TestAndroidLibraryWithResources(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "bp2ninja_android_lib_*")
	if err != nil {
		t.Fatalf("Failed to create tempDir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	resDir := filepath.Join(tempDir, "res")
	if err := os.MkdirAll(resDir, 0755); err != nil {
		t.Fatalf("Failed to create resDir: %v", err)
	}
	manifestFile := filepath.Join(tempDir, "AndroidManifestLib.xml")
	if err := os.WriteFile(manifestFile, []byte("<manifest package=\"com.test.lib\"/>"), 0644); err != nil {
		t.Fatalf("Failed to create manifestFile: %v", err)
	}
	javaFile := filepath.Join(tempDir, "Foo.java")
	if err := os.WriteFile(javaFile, []byte("package com.test.lib; public class Foo {}"), 0644); err != nil {
		t.Fatalf("Failed to create javaFile: %v", err)
	}

	outDir := filepath.Join(tempDir, "out")
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "android_library",
		Name: "TestLib",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":          "TestLib",
			"manifest":      "AndroidManifestLib.xml",
			"resource_dirs": []interface{}{"res"},
			"srcs":          []interface{}{"Foo.java"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()
	expectedSrcjar := filepath.Join(outDir, "gen/TestLib/TestLib.srcjar")
	if !strings.Contains(ninjaContent, expectedSrcjar) {
		t.Errorf("Expected ninja content to reference %s, got:\n%s", expectedSrcjar, ninjaContent)
	}
	if !strings.Contains(ninjaContent, "$aapt2 link --manifest "+manifestFile) {
		t.Errorf("Expected aapt2 link with manifest, got:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "for sj in "+expectedSrcjar) {
		t.Errorf("Expected javac command to extract %s, got:\n%s", expectedSrcjar, ninjaContent)
	}
}

func TestJavaPluginAndAnnotationProcessing(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Mock kotlinc libraries
	kotlincLibDir := filepath.Join(tempDir, "external", "kotlinc", "lib")
	if err := os.MkdirAll(kotlincLibDir, 0755); err != nil {
		t.Fatalf("Failed to create kotlinc lib dir: %v", err)
	}
	kaptJar := filepath.Join(kotlincLibDir, "kotlin-annotation-processing.jar")
	if err := os.WriteFile(kaptJar, []byte("fake-kapt-jar"), 0644); err != nil {
		t.Fatalf("Failed to write kaptJar: %v", err)
	}
	stdlibJar := filepath.Join(kotlincLibDir, "kotlin-stdlib.jar")
	if err := os.WriteFile(stdlibJar, []byte("fake-stdlib-jar"), 0644); err != nil {
		t.Fatalf("Failed to write stdlibJar: %v", err)
	}
	annotJar := filepath.Join(kotlincLibDir, "annotations-13.0.jar")
	if err := os.WriteFile(annotJar, []byte("fake-annot-jar"), 0644); err != nil {
		t.Fatalf("Failed to write annotJar: %v", err)
	}

	// 2. Mock source files
	ktFile := filepath.Join(tempDir, "MyComponent.kt")
	if err := os.WriteFile(ktFile, []byte("package test\ninterface MyComponent"), 0644); err != nil {
		t.Fatalf("Failed to write ktFile: %v", err)
	}
	javaFile := filepath.Join(tempDir, "MyService.java")
	if err := os.WriteFile(javaFile, []byte("package test; public class MyService {}"), 0644); err != nil {
		t.Fatalf("Failed to write javaFile: %v", err)
	}

	outDir := filepath.Join(tempDir, "out")
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(tempDir, outDir)
	opts.AllowMissingDeps = true
	gen := New(opts, nw, nil)

	// Plugin module
	pluginMod := &eval.EvaluatedModule{
		Type: "java_plugin",
		Name: "dagger2-compiler",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":            "dagger2-compiler",
			"processor_class": "dagger.internal.codegen.ComponentProcessor",
			"generates_api":   true,
			"srcs":            []interface{}{"Plugin.java"},
		},
	}
	pluginFile := filepath.Join(tempDir, "Plugin.java")
	if err := os.WriteFile(pluginFile, []byte("package dagger; public class Plugin {}"), 0644); err != nil {
		t.Fatalf("Failed to write pluginFile: %v", err)
	}

	// Mixed Kotlin + Java module with plugin
	mixedMod := &eval.EvaluatedModule{
		Type: "android_library",
		Name: "SystemUIUnfoldLib",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":         "SystemUIUnfoldLib",
			"srcs":         []interface{}{"MyComponent.kt", "MyService.java"},
			"plugins":      []interface{}{"dagger2-compiler"},
			"kotlincflags": []interface{}{"-Xjvm-default=all"},
		},
	}

	// Pure Java module with plugin
	javaMod := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "PureJavaLib",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":    "PureJavaLib",
			"srcs":    []interface{}{"MyService.java"},
			"plugins": []interface{}{"dagger2-compiler"},
		},
	}

	// Module exporting plugin
	exportingMod := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "ExportingLib",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":             "ExportingLib",
			"srcs":             []interface{}{"MyService.java"},
			"exported_plugins": []interface{}{"dagger2-compiler"},
		},
	}

	// Consumer inheriting exported plugin
	consumerMod := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "ConsumerLib",
		Dir:  tempDir,
		Properties: map[string]interface{}{
			"name":        "ConsumerLib",
			"srcs":        []interface{}{"MyService.java"},
			"static_libs": []interface{}{"ExportingLib"},
		},
	}

	modules := []*eval.EvaluatedModule{pluginMod, mixedMod, javaMod, exportingMod, consumerMod}
	if err := gen.Generate(modules); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	ninjaContent := buf.String()

	// 1. Verify java_plugin generates its jar
	pluginJar := filepath.Join(outDir, "lib64", "dagger2-compiler.jar")
	if !strings.Contains(ninjaContent, "build "+pluginJar+": javac_compile") {
		t.Errorf("Expected java_plugin target %s, got:\n%s", pluginJar, ninjaContent)
	}

	// 2. Verify mixed Kotlin/Java module triggers kotlin_java_kapt_compile rule
	mixedJar := filepath.Join(outDir, "lib64", "SystemUIUnfoldLib.jar")
	if !strings.Contains(ninjaContent, "build "+mixedJar+": kotlin_java_kapt_compile") {
		t.Errorf("Expected kotlin_java_kapt_compile rule for SystemUIUnfoldLib, got:\n%s", ninjaContent)
	}

	// Verify KAPT stubs & javac APT command pipeline
	if !strings.Contains(ninjaContent, "-Xplugin="+kaptJar) {
		t.Errorf("Expected -Xplugin flag pointing to kapt jar, got:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "aptMode=stubs") {
		t.Errorf("Expected aptMode=stubs in kapt command, got:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "dagger.internal.codegen.ComponentProcessor") {
		t.Errorf("Expected ComponentProcessor in kapt command, got:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "-proc:only") {
		t.Errorf("Expected javac -proc:only step, got:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "-proc:none") {
		t.Errorf("Expected javac -proc:none final compilation step, got:\n%s", ninjaContent)
	}

	// 3. Verify pure Java module triggers javac_compile with -processorpath and -processor
	pureJar := filepath.Join(outDir, "lib64", "PureJavaLib.jar")
	if !strings.Contains(ninjaContent, "build "+pureJar+": javac_compile") {
		t.Errorf("Expected javac_compile for PureJavaLib, got:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "-processorpath ") || !strings.Contains(ninjaContent, "-processor dagger.internal.codegen.ComponentProcessor") {
		t.Errorf("Expected -processorpath and -processor for PureJavaLib, got:\n%s", ninjaContent)
	}

	// 4. Verify consumer module inherited exported_plugin
	consumerClassesJar := filepath.Join(outDir, "classes", "ConsumerLib", "classes.jar")
	if !strings.Contains(ninjaContent, "build "+consumerClassesJar+": javac_compile") {
		t.Errorf("Expected javac_compile for ConsumerLib, got:\n%s", ninjaContent)
	}
	if !strings.Contains(ninjaContent, "-processor dagger.internal.codegen.ComponentProcessor") {
		t.Errorf("Expected inherited processor in ConsumerLib, got:\n%s", ninjaContent)
	}
}

