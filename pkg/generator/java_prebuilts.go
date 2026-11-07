package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// soongPrebuiltJarAliases maps conceptual Blueprint/Soong module names
// to standard physical prebuilt JAR filenames created by Extractive APEXes
// and prebuilt shadow repositories.
var soongPrebuiltJarAliases = map[string][]string{
	// ART and Libcore
	"art.module.public.api.stubs.module_lib": {"art-module-lib.jar", "art.jar"},
	"art.module.public.api.stubs.system":     {"art-system.jar", "art.jar"},
	"art.module.public.api.stubs":            {"art-public.jar", "art.jar"},
	"art.module.public.api":                  {"art-public.jar", "art-module-lib.jar", "art.jar"},
	"core-libart":                            {"core-libart.jar"},
	"core-oj":                                {"core-oj.jar"},
	"core.current.stubs":                     {"core-libart.jar", "core-oj.jar", "core.jar"},
	"core.platform.api.stubs":                {"core-libart.jar", "core-oj.jar", "art-module-lib.jar"},
	"legacy.core.platform.api.stubs":         {"core-libart.jar", "core-oj.jar"},
	"service-art":                            {"service-art.jar"},

	// Conscrypt & BouncyCastle
	"conscrypt.module.platform.api.stubs": {"conscrypt-module-lib.jar", "conscrypt-coreplatform.jar"},
	"conscrypt.module.public.api.stubs":   {"conscrypt-public.jar", "conscrypt-unbundled.jar"},
	"conscrypt-coreplatform":              {"conscrypt-coreplatform.jar", "conscrypt.jar"},
	"conscrypt-module-lib":                {"conscrypt-module-lib.jar", "conscrypt.jar"},
	"conscrypt":                           {"conscrypt-unbundled.jar", "conscrypt-public.jar", "conscrypt.jar"},
	"bouncycastle_unbundled":              {"bouncycastle-unbundled.jar"},
	"bouncycastle":                        {"bouncycastle-unbundled.jar"},
	"runtime-i18n":                        {"runtime-i18n.jar", "core-icu4j.jar"},

	// Network & Connectivity
	"framework-connectivity":   {"framework-connectivity.jar"},
	"framework-connectivity-t": {"framework-connectivity-t.jar"},
	"framework-tethering":      {"framework-tethering.jar"},

	// Framework Modular Subsystems
	"framework-graphics":       {"framework-graphics.jar"},
	"framework-wifi":           {"framework-wifi.jar"},
	"framework-bluetooth":      {"framework-bluetooth.jar"},
	"framework-media":          {"framework-media.jar"},
	"framework-statsd":         {"framework-statsd.jar"},
	"framework-virtualization": {"framework-virtualization.jar"},
	"framework-nfc":            {"framework-nfc.jar"},
	"framework-permission":     {"framework-permission.jar"},
	"framework-permission-s":   {"framework-permission-s.jar"},
	"framework-res":            {"framework-res-package.jar", "framework-res.jar"},
	"service-permission":       {"service-permission.jar", "service-permission-stubs.jar"},

	// External libraries
	"okhttp":                   {"okhttp-2.7.5.jar", "okhttp.jar"},
	"okhttp-norepackage":       {"okhttp-norepackage-2.7.5.jar", "okhttp-2.7.5.jar"},
	"apache-xml":               {"apache-xml.jar"},
	"libphonenumber":           {"libphonenumber.jar"},
	"org.apache.commons.math":  {"commons-math-2.2.jar", "commons-math.jar", "apache-commons-math.jar"},
	"apache-commons-math":      {"commons-math-2.2.jar", "commons-math.jar", "apache-commons-math.jar"},
	"icu4j_calendar_astronomer": {"icu4j-70.1.jar", "icu4j.jar", "runtime-i18n.jar"},

	// AndroidX and Jetpack Annotations
	"androidx.annotation_annotation": {"annotation-jvm.jar", "annotation.jar", "androidx.annotation.jar"},
	"androidx.annotation":            {"annotation-jvm.jar", "annotation.jar", "androidx.annotation.jar"},
	"annotation":                     {"annotation-jvm.jar", "annotation.jar"},

	// Repackaged BouncyCastle & Crypto
	"bouncycastle-repackaged-unbundled": {"bouncycastle-unbundled.jar", "bouncycastle.jar"},

	// External libraries & utils
	"tagsoup":                 {"tagsoup.jar"},
	"rappor":                  {"rappor.jar"},
	"libphonenumber-platform": {"libphonenumber-platform.jar", "libphonenumber.jar"},
	"libprotobuf-java-nano":   {"protobuf-java-3.25.3.jar", "libprotobuf-java-nano.jar"},
	"libprotobuf-java-lite":   {"protobuf-javalite-3.25.3.jar", "libprotobuf-java-lite.jar"},

	// Framework composite stubs and aggregate libraries
	"ext": {"libphonenumber-platform.jar", "tagsoup.jar", "rappor.jar"},
	"framework-updatable-stubs-module_libs_api": {
		"framework-adservices.jar",
		"framework-appsearch.jar",
		"framework-bluetooth.jar",
		"framework-configinfrastructure.jar",
		"framework-connectivity.jar",
		"framework-connectivity-t.jar",
		"framework-devicelock.jar",
		"framework-graphics.jar",
		"framework-healthfitness.jar",
		"framework-media.jar",
		"framework-mediaprovider.jar",
		"framework-nfc.jar",
		"framework-ondevicepersonalization.jar",
		"framework-permission.jar",
		"framework-permission-s.jar",
		"framework-scheduling.jar",
		"framework-sdkextensions.jar",
		"framework-sdksandbox.jar",
		"framework-statsd.jar",
		"framework-tethering.jar",
		"framework-uwb.jar",
		"framework-virtualization.jar",
		"framework-wifi.jar",
		"android.net.ipsec.ike.jar",
	},
}

// indexJar adds a physical jar path to the Generator's prebuilt jar lookup index.
func (g *Generator) indexJar(path string) {
	if fi, err := os.Stat(path); err != nil || fi.IsDir() || !strings.HasSuffix(path, ".jar") || strings.HasSuffix(path, "-sources.jar") || strings.HasSuffix(path, "-src.jar") {
		return
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	base := filepath.Base(absPath)
	clean := strings.TrimSuffix(base, ".jar")

	newFi, _ := os.Stat(absPath)
	newSize := int64(0)
	if newFi != nil {
		newSize = newFi.Size()
	}

	addKey := func(k string) {
		if k == "" {
			return
		}
		insertOrUpdate := func(key string) {
			if existing, exists := g.prebuiltJarIndex[key]; exists {
				// Prevent tiny dummy jars (< 500 bytes, e.g. public empty manifests)
				// from overriding real implementation/stub jars (>= 500 bytes).
				if exFi, err := os.Stat(existing); err == nil {
					if exFi.Size() < 500 && newSize >= 500 {
						g.prebuiltJarIndex[key] = absPath
					}
				}
			} else {
				g.prebuiltJarIndex[key] = absPath
			}
		}

		insertOrUpdate(k)
		insertOrUpdate(strings.ToLower(k))
	}

	addKey(base)
	addKey(clean)

	// Strip version suffix if present: e.g. "annotation-jvm-1.8.0-alpha01" -> "annotation-jvm"
	// Also "protobuf-java-3.25.3" -> "protobuf-java"
	for i := 0; i < len(clean); i++ {
		if clean[i] >= '0' && clean[i] <= '9' && i > 1 && (clean[i-1] == '-' || clean[i-1] == '_') {
			unver := clean[:i-1]
			addKey(unver)
			addKey(unver + ".jar")
			if strings.HasSuffix(unver, "-jvm") || strings.HasSuffix(unver, "-android") {
				baseUnver := strings.TrimSuffix(strings.TrimSuffix(unver, "-jvm"), "-android")
				addKey(baseUnver)
				addKey(baseUnver + ".jar")
			}
			break
		}
	}

	// Extractive APEX naming conventions: <pkg>__<artifact>-<version>.jar
	if strings.Contains(clean, "__") {
		parts := strings.SplitN(clean, "__", 2)
		pkgPart := parts[0]
		artPart := parts[1]
		addKey(pkgPart)
		addKey(strings.ReplaceAll(pkgPart, "-", "_"))
		addKey(strings.ReplaceAll(pkgPart, "_", "-"))
		addKey(artPart)
		addKey(artPart + ".jar")
		if idx := strings.LastIndex(artPart, "-"); idx != -1 {
			prefix := artPart[:idx]
			addKey(prefix)
			addKey(prefix + ".jar")
		}
	}

	// Maven repository layout indexing:
	// e.g. .../m2repository/androidx/annotation/annotation-jvm/1.8.0-alpha01/annotation-jvm-1.8.0-alpha01.jar
	for _, m2Marker := range []string{"/m2repository/", "/m2/repository/"} {
		if idx := strings.Index(absPath, m2Marker); idx != -1 {
			relM2 := absPath[idx+len(m2Marker):]
			dirs := strings.Split(filepath.Dir(relM2), string(filepath.Separator))
			if len(dirs) >= 3 {
				// dirs: [..., group, artifact, version]
				artifact := dirs[len(dirs)-2]
				group := strings.Join(dirs[:len(dirs)-2], ".")
				soongName := group + "_" + artifact
				addKey(soongName)
				addKey(soongName + ".jar")
				addKey(group + "." + artifact)
				cleanArtifact := strings.TrimSuffix(strings.TrimSuffix(artifact, "-jvm"), "-android")
				if cleanArtifact != artifact {
					addKey(group + "_" + cleanArtifact)
					addKey(group + "_" + cleanArtifact + ".jar")
					addKey(group + "." + cleanArtifact)
					addKey(cleanArtifact)
					addKey(cleanArtifact + ".jar")
				}
			}
			break
		}
	}

	// Index by parent directory name strictly if layout is share/<pkgname>/<file>.jar
	dir := filepath.Dir(absPath)
	grandparent := filepath.Base(filepath.Dir(dir))
	parent := filepath.Base(dir)
	if grandparent == "share" && parent != "" && parent != "." && parent != "share" {
		addKey(parent)
		addKey(strings.ReplaceAll(parent, "-", "_"))
		addKey(strings.ReplaceAll(parent, "_", "-"))
	}

	// Index SDK stub scopes:
	// e.g. .../prebuilts/sdk/34/system-server/service-rkp.jar -> service-rkp.stubs.system_server, service-rkp.stubs
	// e.g. .../prebuilts/sdk/current/module-lib/art.jar -> art.stubs.module_lib
	if strings.Contains(absPath, "/prebuilts/sdk/") {
		switch parent {
		case "public", "system", "module-lib", "system-server", "test", "system_server", "module_lib":
			normScope := strings.ReplaceAll(parent, "-", "_")
			addKey(clean + ".stubs." + parent)
			addKey(clean + ".stubs." + normScope)
			addKey(clean + ".stubs")
			baseClean := strings.TrimSuffix(clean, "-stubs")
			if baseClean != clean {
				addKey(baseClean + ".stubs." + parent)
				addKey(baseClean + ".stubs." + normScope)
				addKey(baseClean + ".stubs")
			}
		}
	}

	// General stubs suffix indexing: e.g. "framework-sdksandbox-stubs" -> "framework-sdksandbox"
	if strings.HasSuffix(clean, "-stubs") {
		baseClean := strings.TrimSuffix(clean, "-stubs")
		addKey(baseClean)
		addKey(baseClean + ".jar")
	}
}

// scanDirForJars scans a directory recursively for .jar files and indexes them.
func (g *Generator) scanDirForJars(dir string, maxDepth int) {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return
	}

	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "out" || name == "build" || name == ".srccache" {
				return filepath.SkipDir
			}
			if maxDepth > 0 {
				rel, err := filepath.Rel(dir, p)
				if err == nil && strings.Count(rel, string(filepath.Separator)) >= maxDepth {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(info.Name(), ".jar") && !strings.HasSuffix(info.Name(), "-sources.jar") && !strings.HasSuffix(info.Name(), "-src.jar") {
			g.indexJar(p)
		}
		return nil
	})
}

// initPrebuiltJarIndex initializes the lookup cache for physical prebuilt JARs.
// It scans explicit CLI paths, in-tree target shadows, and host/container extracted APEX directories.
func (g *Generator) initPrebuiltJarIndex() {
	if g.prebuiltJarIndex != nil {
		return
	}
	g.prebuiltJarIndex = make(map[string]string)

	// 1. Explicit CLI inputs (-bootclasspath, -extra-jars, -prebuilt-jars)
	for _, p := range g.opts.BootClasspath {
		if strings.HasSuffix(p, ".jar") {
			g.indexJar(p)
		} else {
			g.scanDirForJars(p, 5)
		}
	}
	for _, p := range g.opts.ExtraJars {
		if strings.HasSuffix(p, ".jar") {
			g.indexJar(p)
		} else {
			g.scanDirForJars(p, 5)
		}
	}
	for _, p := range g.opts.PrebuiltJarDirs {
		if strings.HasSuffix(p, ".jar") {
			g.indexJar(p)
		} else {
			g.scanDirForJars(p, 5)
		}
	}

	// 2. PrebuiltLibDir
	if g.opts.PrebuiltLibDir != "" {
		g.scanDirForJars(g.opts.PrebuiltLibDir, 2)
	}

	// 3. Extracted APEX directories in sysroot / host container (/apex/*/share, /apex/share)
	for _, apexRoot := range []string{"/apex", filepath.Join(g.opts.SysrootDir, "share")} {
		if fi, err := os.Stat(apexRoot); err == nil && fi.IsDir() {
			g.scanDirForJars(apexRoot, 10)
		}
	}

	// 4. In-tree extractive APEX prebuilts: ${TopDir}/prebuilts/target, host, external (authentic platform precedence)
	if g.opts.TopDir != "" {
		for _, sub := range []string{"prebuilts/target", "prebuilts/host", "prebuilts/external", "prebuilts/misc"} {
			shadowDir := filepath.Join(g.opts.TopDir, sub)
			if fi, err := os.Stat(shadowDir); err == nil && fi.IsDir() {
				g.scanDirForJars(shadowDir, 10)
			}
		}

		// 5. In-tree SDK fallback stubs: ${TopDir}/prebuilts/sdk/<api>/...
		targetApi := g.opts.APILevel
		if targetApi <= 0 {
			targetApi = 34
		}
		for _, sub := range []string{
			filepath.Join("prebuilts/sdk", fmt.Sprintf("%d", targetApi)),
			"prebuilts/sdk/current",
			"prebuilts/sdk/extensions/11",
			"prebuilts/sdk/extensions/7",
		} {
			sdkDir := filepath.Join(g.opts.TopDir, sub)
			if fi, err := os.Stat(sdkDir); err == nil && fi.IsDir() {
				g.scanDirForJars(sdkDir, 10)
			}
		}
	}
}

// resolvePhysicalJars attempts to resolve a conceptual Soong library name or filename
// to all authentic physical JARs located on disk (supporting 1-to-many aggregate aliases).
func (g *Generator) resolvePhysicalJars(libName string) []string {
	g.initPrebuiltJarIndex()

	cleanLib := strings.TrimSuffix(libName, ".jar")

	// Strip common SDK scope prefixes (e.g. "sdk_module-lib_current_framework-wifi" -> "framework-wifi")
	for _, prefix := range []string{
		"sdk_module-lib_current_",
		"sdk_module-lib_34_",
		"sdk_public_current_",
		"sdk_public_34_",
		"sdk_system_current_",
		"sdk_system_34_",
		"sdk_system-server_current_",
		"sdk_system-server_34_",
	} {
		if strings.HasPrefix(cleanLib, prefix) {
			cleanLib = strings.TrimPrefix(cleanLib, prefix)
			break
		}
	}

	var results []string
	seen := make(map[string]bool)
	addResult := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			results = append(results, p)
		}
	}

	// 1. Direct match (e.g. "art-module-lib", "art-module-lib.jar", "androidx.annotation_annotation")
	if p, ok := g.prebuiltJarIndex[libName]; ok {
		addResult(p)
	}
	if p, ok := g.prebuiltJarIndex[cleanLib]; ok {
		addResult(p)
	}
	if p, ok := g.prebuiltJarIndex[cleanLib+".jar"]; ok {
		addResult(p)
	}
	if len(results) > 0 {
		return results
	}

	// 2. Canonical Soong alias mapping (1-to-many supported)
	if aliases, ok := soongPrebuiltJarAliases[cleanLib]; ok {
		for _, alias := range aliases {
			cleanAlias := strings.TrimSuffix(alias, ".jar")
			if strings.Contains(cleanLib, "stubs") {
				if p, ok := g.prebuiltJarIndex[alias]; ok {
					addResult(p)
					continue
				} else if p, ok := g.prebuiltJarIndex[cleanAlias]; ok {
					addResult(p)
					continue
				}
			}
			if outs, ok := g.moduleOutputs[cleanAlias]; ok && len(outs) > 0 {
				for _, o := range outs {
					if strings.HasSuffix(o, ".jar") && !g.emittedPhonies[o] {
						addResult(o)
					}
				}
				continue
			}
			if depMod, ok := g.moduleMap[cleanAlias]; ok && depMod.Dir != "tests_stubs" {
				addResult(filepath.Join(g.opts.OutDir, "lib64", cleanAlias+".jar"))
				continue
			}
			if p, ok := g.prebuiltJarIndex[alias]; ok {
				addResult(p)
			} else if p, ok := g.prebuiltJarIndex[cleanAlias]; ok {
				addResult(p)
			}
		}
		if len(results) > 0 {
			return results
		}
	}

	// 3. Generic suffix stripping for standard implementation and stub scopes (.impl, .stubs.*, -stubs)
	for _, suffix := range []string{
		".impl",
		".stubs.exportable.module_lib",
		".stubs.exportable.system",
		".stubs.exportable.test",
		".stubs.exportable",
		".stubs.module_lib",
		".stubs.system_server",
		".stubs.system",
		".stubs.test",
		".stubs",
		"-stubs",
	} {
		if strings.HasSuffix(cleanLib, suffix) {
			baseLib := strings.TrimSuffix(cleanLib, suffix)
			if p, ok := g.prebuiltJarIndex[baseLib+".jar"]; ok {
				addResult(p)
			} else if p, ok := g.prebuiltJarIndex[baseLib]; ok {
				addResult(p)
			}
			if aliases, ok := soongPrebuiltJarAliases[baseLib]; ok {
				for _, alias := range aliases {
					cleanAlias := strings.TrimSuffix(alias, ".jar")
					if p, ok := g.prebuiltJarIndex[alias]; ok {
						addResult(p)
					} else if p, ok := g.prebuiltJarIndex[cleanAlias]; ok {
						addResult(p)
					}
				}
			}
			if len(results) > 0 {
				return results
			}
		}
	}

	// 4. Fallback: check if libName is a literal path on disk
	if fi, err := os.Stat(libName); err == nil && !fi.IsDir() && strings.HasSuffix(libName, ".jar") {
		if abs, err := filepath.Abs(libName); err == nil {
			addResult(abs)
		} else {
			addResult(libName)
		}
	}
	if g.opts.TopDir != "" {
		topCand := filepath.Join(g.opts.TopDir, libName)
		if fi, err := os.Stat(topCand); err == nil && !fi.IsDir() && strings.HasSuffix(topCand, ".jar") {
			addResult(topCand)
		}
	}

	return results
}

// resolvePhysicalJar attempts to resolve a conceptual Soong library name or filename
// to an authentic physical JAR located on disk.
func (g *Generator) resolvePhysicalJar(libName string) string {
	jars := g.resolvePhysicalJars(libName)
	if len(jars) > 0 {
		return jars[0]
	}
	return ""
}
