package generator

import (
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
	"conscrypt":                           {"conscrypt-unbundled.jar", "conscrypt-public.jar", "conscrypt.jar"},
	"bouncycastle_unbundled":              {"bouncycastle-unbundled.jar"},
	"bouncycastle":                        {"bouncycastle-unbundled.jar"},

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
	"framework-res":            {"framework-res-package.jar", "framework-res.jar"},
	"service-permission":       {"service-permission-stubs.jar", "service-permission.jar"},

	// External libraries
	"okhttp":             {"okhttp-2.7.5.jar", "okhttp.jar"},
	"okhttp-norepackage": {"okhttp-norepackage-2.7.5.jar", "okhttp-2.7.5.jar"},
	"apache-xml":         {"apache-xml.jar"},
	"libphonenumber":     {"libphonenumber.jar"},
}

// indexJar adds a physical jar path to the Generator's prebuilt jar lookup index.
func (g *Generator) indexJar(path string) {
	if fi, err := os.Stat(path); err != nil || fi.IsDir() || !strings.HasSuffix(path, ".jar") {
		return
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	base := filepath.Base(absPath)
	clean := strings.TrimSuffix(base, ".jar")

	if _, exists := g.prebuiltJarIndex[base]; !exists {
		g.prebuiltJarIndex[base] = absPath
	}
	if _, exists := g.prebuiltJarIndex[clean]; !exists {
		g.prebuiltJarIndex[clean] = absPath
	}
	lowerBase := strings.ToLower(base)
	if _, exists := g.prebuiltJarIndex[lowerBase]; !exists {
		g.prebuiltJarIndex[lowerBase] = absPath
	}
	lowerClean := strings.ToLower(clean)
	if _, exists := g.prebuiltJarIndex[lowerClean]; !exists {
		g.prebuiltJarIndex[lowerClean] = absPath
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
		if strings.HasSuffix(info.Name(), ".jar") {
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

	// 3. In-tree extractive APEX prebuilts: ${TopDir}/prebuilts/target
	if g.opts.TopDir != "" {
		targetShadowsDir := filepath.Join(g.opts.TopDir, "prebuilts/target")
		if fi, err := os.Stat(targetShadowsDir); err == nil && fi.IsDir() {
			g.scanDirForJars(targetShadowsDir, 8)
		}
	}

	// 4. Extracted APEX directories in sysroot / host container (/apex/*/share, /apex/share)
	for _, apexRoot := range []string{"/apex", filepath.Join(g.opts.SysrootDir, "share")} {
		if fi, err := os.Stat(apexRoot); err == nil && fi.IsDir() {
			g.scanDirForJars(apexRoot, 5)
		}
	}
}

// resolvePhysicalJar attempts to resolve a conceptual Soong library name or filename
// to an authentic physical JAR located on disk.
func (g *Generator) resolvePhysicalJar(libName string) string {
	g.initPrebuiltJarIndex()

	cleanLib := strings.TrimSuffix(libName, ".jar")

	// 1. Direct match (e.g. "art-module-lib", "art-module-lib.jar")
	if p, ok := g.prebuiltJarIndex[libName]; ok {
		return p
	}
	if p, ok := g.prebuiltJarIndex[cleanLib]; ok {
		return p
	}
	if p, ok := g.prebuiltJarIndex[cleanLib+".jar"]; ok {
		return p
	}

	// 2. Canonical Soong alias mapping (e.g. "art.module.public.api.stubs.module_lib" -> "art-module-lib.jar")
	if aliases, ok := soongPrebuiltJarAliases[cleanLib]; ok {
		for _, alias := range aliases {
			if p, ok := g.prebuiltJarIndex[alias]; ok {
				return p
			}
			cleanAlias := strings.TrimSuffix(alias, ".jar")
			if p, ok := g.prebuiltJarIndex[cleanAlias]; ok {
				return p
			}
		}
	}

	// 3. Generic suffix stripping for standard stub scopes (.stubs.module_lib, .stubs.system, .stubs)
	for _, suffix := range []string{".stubs.module_lib", ".stubs.system", ".stubs", "-stubs"} {
		if strings.HasSuffix(cleanLib, suffix) {
			baseLib := strings.TrimSuffix(cleanLib, suffix)
			if p, ok := g.prebuiltJarIndex[baseLib+".jar"]; ok {
				return p
			}
			if p, ok := g.prebuiltJarIndex[baseLib]; ok {
				return p
			}
		}
	}

	// 4. Fallback: check if libName is a literal path on disk
	if fi, err := os.Stat(libName); err == nil && !fi.IsDir() && strings.HasSuffix(libName, ".jar") {
		if abs, err := filepath.Abs(libName); err == nil {
			return abs
		}
		return libName
	}
	if g.opts.TopDir != "" {
		topCand := filepath.Join(g.opts.TopDir, libName)
		if fi, err := os.Stat(topCand); err == nil && !fi.IsDir() && strings.HasSuffix(topCand, ".jar") {
			return topCand
		}
	}

	return ""
}
