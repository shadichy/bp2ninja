package generator

import (
	"bytes"
	"strings"
	"testing"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

func TestIncludePrecedenceVNDKShadowing(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.SysrootDir = "/apex/com.android.runtime.dev"
	opts.DisableSkipAbis = true
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libsurface",
		Properties: map[string]interface{}{
			"name": "libsurface",
			"srcs": []interface{}{"surface.cpp"},
			"include_dirs": []interface{}{
				"/opt/android-sdk/vndk/v34/include",
				"/apex/include",
				"/apex/com.android.ext.framework.base/include",
				"include/local",
			},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()
	ccIdx := strings.Index(out, "build out/obj/libsurface/surface.o: cxx_compile")
	if ccIdx < 0 {
		t.Fatalf("Expected cxx_compile edge for surface.cpp")
	}
	compileSection := out[ccIdx:]
	if next := strings.Index(compileSection[1:], "\nbuild "); next >= 0 {
		compileSection = compileSection[:next+1]
	}

	// Verify APEX headers appear and VNDK headers are suppressed when DisableSkipAbis is set
	if !strings.Contains(compileSection, "-I/apex/com.android.ext.framework.base/include") {
		t.Errorf("Expected authentic APEX framework include in compile flags")
	}
	if !strings.Contains(compileSection, "-I/apex/include") {
		t.Errorf("Expected authentic APEX include in compile flags")
	}
	if strings.Contains(compileSection, "/opt/android-sdk/vndk/v34/include") {
		t.Errorf("VNDK snapshot header should be suppressed when authentic APEX headers are present with DisableSkipAbis")
	}

	// Now test ordering when DisableSkipAbis is false and SkipAbiLevel > 0
	var buf2 bytes.Buffer
	nw2 := ninja.NewWriter(&buf2)
	opts2 := DefaultOptions(".", "out")
	opts2.SysrootDir = "/apex/com.android.runtime.dev"
	opts2.SkipAbiLevel = 3
	opts2.DisableSkipAbis = false
	gen2 := New(opts2, nw2, nil)

	if err := gen2.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	out2 := buf2.String()
	ccIdx2 := strings.Index(out2, "build out/obj/libsurface/surface.o: cxx_compile")
	if ccIdx2 < 0 {
		t.Fatalf("Expected cxx_compile edge")
	}
	section2 := out2[ccIdx2:]
	apexPos := strings.Index(section2, "-I/apex/include")
	vndkPos := strings.Index(section2, "-I/opt/android-sdk/vndk/v34/include")

	if apexPos < 0 || vndkPos < 0 {
		t.Fatalf("Expected both APEX and VNDK headers in output")
	}
	if apexPos >= vndkPos {
		t.Errorf("APEX header position (%d) must precede VNDK header position (%d)", apexPos, vndkPos)
	}
}

func TestJavaToolchainUTF8Encoding(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.JavaEncoding = "UTF-8"
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "java_library",
		Name: "libmyjava",
		Properties: map[string]interface{}{
			"name": "libmyjava",
			"srcs": []interface{}{"A.java", "gen.srcjar"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()

	// Check rule definition contains UTF-8 environment exports
	if !strings.Contains(out, "LC_ALL=C.UTF-8") || !strings.Contains(out, "JAVA_TOOL_OPTIONS=\"-Dfile.encoding=UTF-8\"") {
		t.Errorf("Expected UTF-8 environment exports in Java compilation rule")
	}
	if !strings.Contains(out, "-encoding UTF-8") {
		t.Errorf("Expected -encoding UTF-8 in javac invocation")
	}

	// Check .srcjar handling
	if !strings.Contains(out, "unzip -qo") {
		t.Errorf("Expected srcjar unzipping command in Java compile edge")
	}
}

func TestPlatformSysrootCrtResolution(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	opts.SysrootDir = "/apex/com.android.runtime.dev"
	gen := New(opts, nw, nil)

	crtMod := &eval.EvaluatedModule{
		Type: "cc_object",
		Name: "crtbegin_so",
		Properties: map[string]interface{}{
			"name": "crtbegin_so",
			"srcs": []interface{}{"crtbegin_so.S"},
		},
	}

	binMod := &eval.EvaluatedModule{
		Type: "cc_binary",
		Name: "mydaemon",
		Properties: map[string]interface{}{
			"name": "mydaemon",
			"srcs": []interface{}{"main.cpp"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{crtMod, binMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()

	// cc_object emits object file
	if !strings.Contains(out, "build out/lib64/crtbegin_so.o:") && !strings.Contains(out, "build out/obj/crtbegin_so/crtbegin_so.o:") {
		t.Errorf("Expected crtbegin_so.o output")
	}

	// cc_binary links with platform sysroot flags
	binEdge := linkEdgeSection(out, "build out/bin/mydaemon: link_binary")
	if binEdge == "" {
		t.Fatalf("Expected link_binary edge for mydaemon")
	}
	if !strings.Contains(binEdge, "--sysroot=/apex/com.android.runtime.dev") {
		t.Errorf("Expected --sysroot flag in binary ldflags")
	}
	if !strings.Contains(binEdge, "-L/apex/com.android.runtime.dev/lib64") || !strings.Contains(binEdge, "-B/apex/com.android.runtime.dev/lib64") {
		t.Errorf("Expected -L and -B flags pointing to sysroot lib64")
	}
}

func TestXsdConfigAllFlagsAndTaggedRefs(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	xsdMod := &eval.EvaluatedModule{
		Type: "xsd_config",
		Name: "display-device-config",
		Properties: map[string]interface{}{
			"name":           "display-device-config",
			"srcs":           []interface{}{"display_device_config.xsd"},
			"package_name":   "com.android.server.display.config",
			"gen_writer":     true,
			"root_elements":  []interface{}{"displayConfiguration"},
			"boolean_getter": true,
			"tinyxml":        true,
		},
	}

	consumerMod := &eval.EvaluatedModule{
		Type: "cc_library",
		Name: "libconsumer",
		Properties: map[string]interface{}{
			"name":              "libconsumer",
			"srcs":              []interface{}{"consumer.cpp", ":display-device-config{cpp}"},
			"generated_headers": []interface{}{"display-device-config"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{xsdMod, consumerMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()

	// Check xsdc rules and flags
	if !strings.Contains(out, "out/gen/display-device-config/cpp/com_android_server_display_config.cpp") ||
		!strings.Contains(out, "out/gen/display-device-config/cpp/include/com_android_server_display_config.h") {
		t.Errorf("Expected xsdc_cpp edge with cpp and h outputs")
	}
	if !strings.Contains(out, "package_name = com.android.server.display.config") {
		t.Errorf("Expected package flag in xsdc edge")
	}
	if !strings.Contains(out, "-w") {
		t.Errorf("Expected -w (gen_writer) in xsdc edge")
	}
	if !strings.Contains(out, "-r displayConfiguration") {
		t.Errorf("Expected -r root_elements in xsdc edge")
	}
	if !strings.Contains(out, "-b") {
		t.Errorf("Expected -b boolean_getter in xsdc edge")
	}
	if !strings.Contains(out, "-t") {
		t.Errorf("Expected -t tinyxml in xsdc edge")
	}

	// Java srcjar
	if !strings.Contains(out, "build out/gen/display-device-config/java/com_android_server_display_config_xsdcgen.srcjar: xsdc_java") {
		t.Errorf("Expected xsdc_java srcjar output")
	}

	// Schema copy
	if !strings.Contains(out, "build out/gen/display-device-config/display-device-config.xsd: xsd_copy") {
		t.Errorf("Expected schema copy edge")
	}

	// Tagged output resolution in consumer
	if !strings.Contains(out, "out/gen/display-device-config/cpp/com_android_server_display_config.cpp") {
		t.Errorf("Expected consumer to compile resolved tagged src out/gen/display-device-config/cpp/com_android_server_display_config.cpp")
	}

	// Generated header include
	ccIdx := strings.Index(out, "build out/obj/libconsumer/consumer.o: cxx_compile")
	if ccIdx < 0 {
		t.Fatalf("Expected cxx_compile edge for consumer.cpp")
	}
	if !strings.Contains(out[ccIdx:], "-Iout/gen/display-device-config/cpp/include") {
		t.Errorf("Expected consumer include flags to have xsd_config cpp/include")
	}
}

func TestLinkerConfigGeneration(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "linker_config",
		Name: "system_linker_config",
		Properties: map[string]interface{}{
			"name":        "system_linker_config",
			"src":         "linker.config.json",
			"installable": true,
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "build out/gen/system_linker_config/linker.config.pb: conv_linker_config linker.config.json") {
		t.Errorf("Expected conv_linker_config edge producing out/gen/system_linker_config/linker.config.pb, got:\n%s", out)
	}
	if !strings.Contains(out, "build out/etc/linker.config.pb: copy out/gen/system_linker_config/linker.config.pb") {
		t.Errorf("Expected copy edge to out/etc/linker.config.pb, got:\n%s", out)
	}
}

func TestPrebuiltEtcXmlValidation(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	modXsd := &eval.EvaluatedModule{
		Type: "prebuilt_etc_xml",
		Name: "permissions_xml",
		Properties: map[string]interface{}{
			"name":   "permissions_xml",
			"src":    "privapp-permissions.xml",
			"schema": "frameworks/base/data/etc/privapp-permissions.xsd",
		},
	}

	modDtd := &eval.EvaluatedModule{
		Type: "prebuilt_etc_xml",
		Name: "audio_policy_xml",
		Properties: map[string]interface{}{
			"name": "audio_policy_xml",
			"src":  "audio_policy.xml",
			"dtd":  "frameworks/av/services/audiopolicy/audio_policy.dtd",
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{modXsd, modDtd}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "build out/etc/privapp-permissions.xml: xmllint_xsd privapp-permissions.xml | frameworks/base/data/etc/privapp-permissions.xsd") {
		t.Errorf("Expected xmllint_xsd edge for permissions_xml, got:\n%s", out)
	}
	if !strings.Contains(out, "build out/etc/audio_policy.xml: xmllint_dtd audio_policy.xml | frameworks/av/services/audiopolicy/audio_policy.dtd") {
		t.Errorf("Expected xmllint_dtd edge for audio_policy_xml, got:\n%s", out)
	}
}

func TestBootimgAndAvbGeneration(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	bootMod := &eval.EvaluatedModule{
		Type: "bootimg",
		Name: "boot",
		Properties: map[string]interface{}{
			"name":            "boot",
			"stem":            "boot.img",
			"kernel_prebuilt": "kernel",
			"ramdisk_module":  "ramdisk.img",
			"cmdline":         "console=ttyS0 earlycon",
			"header_version":  "4",
		},
	}

	vbmetaMod := &eval.EvaluatedModule{
		Type: "vbmeta",
		Name: "vbmeta",
		Properties: map[string]interface{}{
			"name":                    "vbmeta",
			"stem":                    "vbmeta.img",
			"partitions":              []interface{}{"boot", "system"},
			"key":                     "key.pem",
			"algorithm":               "SHA256_RSA2048",
			"rollback_index":          1,
			"rollback_index_location": 1,
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{bootMod, vbmetaMod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "build out/images/boot.img: mkbootimg kernel") {
		t.Errorf("Expected mkbootimg edge for boot image, got:\n%s", out)
	}
	if !strings.Contains(out, "--cmdline \"console=ttyS0 earlycon\"") {
		t.Errorf("Expected cmdline in bootimg edge")
	}
	if !strings.Contains(out, "build out/images/vbmeta.img: avbtool_vbmeta") {
		t.Errorf("Expected avbtool_vbmeta edge for vbmeta image, got:\n%s", out)
	}
	if !strings.Contains(out, "--algorithm SHA256_RSA2048") {
		t.Errorf("Expected algorithm in vbmeta edge")
	}
}

func TestLogicalPartitionLpmake(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "logical_partition",
		Name: "super",
		Properties: map[string]interface{}{
			"name":               "super",
			"stem":               "super.img",
			"size":               4294967296,
			"metadata_size":      65536,
			"metadata_slots":     2,
			"default_group_size": 2147483648,
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "build out/images/super.img: lpmake") {
		t.Errorf("Expected lpmake edge, got:\n%s", out)
	}
	if !strings.Contains(out, "--device-size 4294967296") {
		t.Errorf("Expected --device-size flag")
	}
	if !strings.Contains(out, "--metadata-size 65536") {
		t.Errorf("Expected --metadata-size flag")
	}
	if !strings.Contains(out, "--metadata-slots 2") {
		t.Errorf("Expected --metadata-slots flag")
	}
}

func TestJavaSystemModulesJlink(t *testing.T) {
	var buf bytes.Buffer
	nw := ninja.NewWriter(&buf)
	opts := DefaultOptions(".", "out")
	gen := New(opts, nw, nil)

	mod := &eval.EvaluatedModule{
		Type: "java_system_modules",
		Name: "core-system-modules",
		Properties: map[string]interface{}{
			"name": "core-system-modules",
			"libs": []interface{}{"core.current.stubs"},
		},
	}

	if err := gen.Generate([]*eval.EvaluatedModule{mod}); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "build out/system_modules/core-system-modules: system_modules_jlink") {
		t.Errorf("Expected system_modules_jlink edge, got:\n%s", out)
	}
}
