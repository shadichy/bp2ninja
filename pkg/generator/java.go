package generator

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

var patchJarExportsCache = make(map[string][]string)

func collectPatchJarPackages(jarPath string) []string {
	if pkgs, ok := patchJarExportsCache[jarPath]; ok {
		return pkgs
	}
	zr, err := zip.OpenReader(jarPath)
	if err != nil {
		patchJarExportsCache[jarPath] = nil
		return nil
	}
	defer zr.Close()
	pkgSet := make(map[string]bool)
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".class") && strings.Contains(f.Name, "/") {
			pkg := strings.ReplaceAll(filepath.Dir(f.Name), "/", ".")
			if pkg != "" && pkg != "." && !strings.HasPrefix(pkg, "META-INF") && pkg != "sun.misc" {
				pkgSet[pkg] = true
			}
		}
	}
	var pkgs []string
	for p := range pkgSet {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	patchJarExportsCache[jarPath] = pkgs
	return pkgs
}

func (g *Generator) resolveJarjarRulesFile(mod *eval.EvaluatedModule, jarjarRules string) string {
	if strings.HasPrefix(jarjarRules, ":") {
		cleanRule := strings.TrimPrefix(jarjarRules, ":")
		if fgs, ok := g.filegroups[cleanRule]; ok && len(fgs) > 0 {
			return fgs[0]
		} else if outs, ok := g.moduleOutputs[cleanRule]; ok && len(outs) > 0 {
			return outs[0]
		}
		return filepath.Join(g.opts.OutDir, "gen", cleanRule, cleanRule)
	}
	return filepath.Join(mod.Dir, jarjarRules)
}

func (g *Generator) resolveJarTargets(lib string) []string {
	if lib == "current_android_jar" || lib == ":current_android_jar" || lib == "system_android_jar" || lib == ":system_android_jar" {
		jar := g.resolveCurrentAndroidJar()
		if jar != "" {
			return []string{jar}
		}
		return nil
	}

	cleanLib := strings.TrimSuffix(lib, ".jar")
	var targets []string

	// ---------------------------------------------------------------------
	// Tier 1: Graph-Local Module Outputs (Same Ninja Manifest)
	// ---------------------------------------------------------------------
	if outs, ok := g.moduleOutputs[lib]; ok && len(outs) > 0 {
		for _, o := range outs {
			if strings.HasSuffix(o, ".jar") && !g.emittedPhonies[o] {
				targets = append(targets, o)
			}
		}
	}
	if len(targets) == 0 {
		if outs, ok := g.moduleOutputs[cleanLib]; ok && len(outs) > 0 {
			for _, o := range outs {
				if strings.HasSuffix(o, ".jar") && !g.emittedPhonies[o] {
					targets = append(targets, o)
				}
			}
		}
	}
	if len(targets) == 0 {
		if depMod, ok := g.moduleMap[cleanLib]; ok {
			if depMod.Dir == "tests_stubs" {
				if physJars := g.resolvePhysicalJars(cleanLib); len(physJars) > 0 {
					targets = append(targets, physJars...)
				} else {
					targets = append(targets, filepath.Join(g.opts.OutDir, "lib64", cleanLib+".jar"))
				}
			} else {
				targets = append(targets, filepath.Join(g.opts.OutDir, "lib64", cleanLib+".jar"))
			}
		}
	}

	// Check if dependency is a genrule/java_genrule in moduleMap
	if len(targets) == 0 {
		for _, key := range []string{lib, cleanLib} {
			if depMod, ok := g.moduleMap[key]; ok && (depMod.Type == "genrule" || depMod.Type == "java_genrule") {
				for _, out := range depMod.GetStringList("out") {
					if strings.HasSuffix(out, ".jar") {
						targets = append(targets, filepath.Join(g.opts.OutDir, "gen", key, out))
					}
				}
				if len(targets) > 0 {
					break
				}
			}
		}
	}

	// Check if dependency is an aidl_interface or droidstubs
	if len(targets) == 0 {
		if outs, ok := g.moduleOutputs[lib+"-java"]; ok && len(outs) > 0 {
			for _, o := range outs {
				if strings.HasSuffix(o, ".jar") && !g.emittedPhonies[o] {
					targets = append(targets, o)
				}
			}
		}
	}
	if len(targets) == 0 {
		if depMod, ok := g.moduleMap[lib]; ok {
			if depMod.Type == "aidl_interface" {
				targets = append(targets, filepath.Join(g.libDir(), lib+"-java.jar"))
			} else if depMod.Type == "droidstubs" || depMod.Type == "droiddoc" {
				targets = append(targets, filepath.Join(g.opts.OutDir, "gen", lib, "stubs.srcjar"))
			}
		}
	}

	// ---------------------------------------------------------------------
	// Tier 2: Extractive APEX & Shadow Prebuilt Physical JARs
	// ---------------------------------------------------------------------
	if len(targets) == 0 {
		if physJars := g.resolvePhysicalJars(lib); len(physJars) > 0 {
			targets = append(targets, physJars...)
		}
	}

	return targets
}

func (g *Generator) hasProto(m *eval.EvaluatedModule) bool {
	if m == nil {
		return false
	}
	if pMap := m.GetMap("proto"); pMap != nil {
		return true
	}
	for _, s := range m.GetStringList("srcs") {
		if strings.HasSuffix(s, ".proto") {
			return true
		}
	}
	return false
}

func (g *Generator) resolveStaticLibsJars(mod *eval.EvaluatedModule) []string {
	var jars []string
	visited := make(map[string]bool)
	visited[mod.Name] = true
	var collectStatic func(m *eval.EvaluatedModule)
	collectStatic = func(m *eval.EvaluatedModule) {
		if m == nil {
			return
		}
		for _, sl := range m.GetStringList("static_libs") {
			if !visited[sl] {
				visited[sl] = true
				targets := g.resolveJarTargets(sl)
				for _, t := range targets {
					if !g.emittedPhonies[t] {
						jars = append(jars, t)
					}
				}
				if child, ok := g.moduleMap[sl]; ok {
					collectStatic(child)
				}
			}
		}

		// Proto runtimes are implicitly statically linked in Soong (build/soong/java/proto.go)
		if g.hasProto(m) {
			protoType := "nano"
			if pMap := m.GetMap("proto"); pMap != nil {
				if t, ok := pMap["type"].(string); ok && t != "" {
					protoType = t
				}
			}
			var protoLib string
			switch protoType {
			case "nano":
				protoLib = "libprotobuf-java-nano"
			case "lite":
				protoLib = "libprotobuf-java-lite"
			case "micro":
				protoLib = "libprotobuf-java-micro"
			}
			if protoLib != "" && !visited[protoLib] {
				visited[protoLib] = true
				targets := g.resolveJarTargets(protoLib)
				for _, t := range targets {
					if !g.emittedPhonies[t] {
						jars = append(jars, t)
					}
				}
			}
		}
	}
	collectStatic(mod)
	return dedup(jars)
}

func (g *Generator) buildClasspath(mod *eval.EvaluatedModule) (cpFlag string, cpDeps []string) {
	var cpEntries []string

	// =========================================================================
	// Tier 0: Explicit Bootclasspath and Injected Extra JARs
	// =========================================================================
	for _, bcp := range g.opts.BootClasspath {
		if bcp != "" {
			if fi, err := os.Stat(bcp); err == nil && !fi.IsDir() {
				cpEntries = append(cpEntries, bcp)
				cpDeps = append(cpDeps, bcp)
			}
		}
	}
	for _, ej := range g.opts.ExtraJars {
		if ej != "" {
			baseLib := strings.TrimSuffix(filepath.Base(ej), ".jar")
			if mod.Name == baseLib || g.IsTransitiveInputOf(mod.Name, baseLib, make(map[string]bool)) {
				continue
			}
			if fi, err := os.Stat(ej); err == nil && !fi.IsDir() {
				cpEntries = append(cpEntries, ej)
				cpDeps = append(cpDeps, ej)
			}
		}
	}

	// =========================================================================
	// Tier 1 & Tier 2: Dependencies from libs and static_libs (transitive)
	// =========================================================================
	var depLibs []string
	visited := make(map[string]bool)
	var collectLibs func(m *eval.EvaluatedModule, isDirect bool)
	collectLibs = func(m *eval.EvaluatedModule, isDirect bool) {
		if m == nil {
			return
		}
		if isDirect {
			for _, l := range m.GetStringList("libs") {
				if !visited[l] {
					visited[l] = true
					depLibs = append(depLibs, l)
					if subMod, ok := g.moduleMap[l]; ok {
						collectLibs(subMod, false)
						if subMod.Type == "java_aconfig_library" {
							for _, aconfigDep := range []string{"aconfig-annotations-lib", "unsupportedappusage"} {
								if !visited[aconfigDep] {
									visited[aconfigDep] = true
									depLibs = append(depLibs, aconfigDep)
								}
							}
						}
					}
				}
			}
		}
		for _, sl := range m.GetStringList("static_libs") {
			if !visited[sl] {
				visited[sl] = true
				depLibs = append(depLibs, sl)
				if subMod, ok := g.moduleMap[sl]; ok {
					collectLibs(subMod, false)
					if subMod.Type == "java_aconfig_library" {
						for _, aconfigDep := range []string{"aconfig-annotations-lib", "unsupportedappusage"} {
							if !visited[aconfigDep] {
								visited[aconfigDep] = true
								depLibs = append(depLibs, aconfigDep)
							}
						}
					}
				}
			}
		}

		for _, iface := range m.GetStringList("interfaces") {
			ifaceJava := sanitizeHidlJavaName(iface)
			if !visited[ifaceJava] {
				visited[ifaceJava] = true
				depLibs = append(depLibs, ifaceJava)
				if subMod, ok := g.moduleMap[ifaceJava]; ok {
					collectLibs(subMod, false)
				}
			}
		}

		if g.hasProto(m) {
			protoType := "nano"
			if pMap := m.GetMap("proto"); pMap != nil {
				if t, ok := pMap["type"].(string); ok && t != "" {
					protoType = t
				}
			}
			var protoLib string
			switch protoType {
			case "nano":
				protoLib = "libprotobuf-java-nano"
			case "lite":
				protoLib = "libprotobuf-java-lite"
			case "micro":
				protoLib = "libprotobuf-java-micro"
			}
			if protoLib != "" && !visited[protoLib] {
				visited[protoLib] = true
				depLibs = append(depLibs, protoLib)
			}
		}
	}
	collectLibs(mod, true)
	if mod.Type == "java_sdk_library" && strings.HasPrefix(mod.Dir, "frameworks/base") {
		for _, extra := range []string{"framework-minus-apex", "framework-configinfrastructure"} {
			if !visited[extra] && !g.IsTransitiveInputOf(mod.Name, extra, make(map[string]bool)) {
				visited[extra] = true
				depLibs = append(depLibs, extra)
			}
		}
	}
	for _, lib := range depLibs {
		targets := g.resolveJarTargets(lib)
		cleanLib := strings.TrimSuffix(lib, ".jar")
		if len(targets) == 0 {
			jarTarget := filepath.Join(g.libDir(), cleanLib+".jar")
			targets = append(targets, jarTarget)
			if g.opts.AllowMissingDeps {
				hasJarOutput := false
				for _, o := range append(g.moduleOutputs[lib], g.moduleOutputs[cleanLib]...) {
					if strings.HasSuffix(o, ".jar") {
						hasJarOutput = true
						break
					}
				}
				if !hasJarOutput {
					g.emitPhonyIfNeeded(jarTarget)
				}
			}
		}
		for _, t := range targets {
			cpDeps = append(cpDeps, t)
			cpEntries = append(cpEntries, t)
		}
	}

	// =========================================================================
	// Tier 3: Android SDK android.jar and Platform Classpath (sdk_version aware)
	// =========================================================================
	if !g.opts.IsHost {
		sdkVer := mod.GetString("sdk_version")
		if sdkVer == "" {
			// SdkPrivate / Platform build:
			// Apply g.opts.PlatformClasspath, but skip any jar that depends on mod (avoid cycles)
			for _, pj := range g.opts.PlatformClasspath {
				if pj == "" {
					continue
				}
				baseLib := strings.TrimSuffix(filepath.Base(pj), ".jar")
				if mod.Name == baseLib || g.IsTransitiveInputOf(mod.Name, baseLib, make(map[string]bool)) {
					continue
				}
				cpEntries = append(cpEntries, pj)
				cpDeps = append(cpDeps, pj)
			}
		} else if sdkVer != "none" && sdkVer != "core_platform" {
			androidJar := g.resolveCurrentAndroidJarForModule(mod)
			if androidJar != "" {
				cpEntries = append(cpEntries, androidJar)
				cpDeps = append(cpDeps, androidJar)
			}
		}
	}

	// =========================================================================
	// Tier 4: In-Tree SDK Prebuilts (prebuilts/sdk/<api>/...)
	// =========================================================================
	if g.opts.TopDir != "" {
		targetApi := g.opts.APILevel
		if targetApi <= 0 {
			targetApi = 34
		}
		for _, lib := range depLibs {
			cleanLib := strings.TrimSuffix(lib, ".jar")
			baseLib := cleanLib
			for _, suf := range []string{".stubs.system_server", ".stubs.module_lib", ".stubs.system", ".stubs.test", ".stubs"} {
				if strings.HasSuffix(baseLib, suf) {
					baseLib = strings.TrimSuffix(baseLib, suf)
					break
				}
			}
			var topJarCandidates []string
			for _, name := range []string{cleanLib, baseLib} {
				topJarCandidates = append(topJarCandidates,
					filepath.Join(g.opts.TopDir, "prebuilts/sdk", fmt.Sprintf("%d", targetApi), "system-server", name+".jar"),
					filepath.Join(g.opts.TopDir, "prebuilts/sdk", fmt.Sprintf("%d", targetApi), "module-lib", name+".jar"),
					filepath.Join(g.opts.TopDir, "prebuilts/sdk", fmt.Sprintf("%d", targetApi), "system", name+".jar"),
					filepath.Join(g.opts.TopDir, "prebuilts/sdk", fmt.Sprintf("%d", targetApi), "public", name+".jar"),
					filepath.Join(g.opts.TopDir, "prebuilts/sdk", "current/system-server", name+".jar"),
					filepath.Join(g.opts.TopDir, "prebuilts/sdk", "current/module-lib", name+".jar"),
					filepath.Join(g.opts.TopDir, "prebuilts/sdk", "current/system", name+".jar"),
					filepath.Join(g.opts.TopDir, "prebuilts/sdk", "current/public", name+".jar"),
				)
				if cleanLib == baseLib {
					break
				}
			}
			for _, c := range topJarCandidates {
				if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
					cpEntries = append(cpEntries, c)
					cpDeps = append(cpDeps, c)
					break
				}
			}
		}
	}

	// =========================================================================
	// Tier 5: Environment CLASSPATH fallback
	// =========================================================================
	if envCP := os.Getenv("CLASSPATH"); envCP != "" {
		for _, part := range strings.Split(envCP, ":") {
			if part != "" {
				if fi, err := os.Stat(part); err == nil && !fi.IsDir() {
					cpEntries = append(cpEntries, part)
				}
			}
		}
	}

	cpEntries = dedup(cpEntries)
	cpDeps = dedup(cpDeps)

	if len(cpEntries) > 0 {
		cpFlag = "-cp " + strings.Join(cpEntries, ":")
	}
	return cpFlag, cpDeps
}

func (g *Generator) compileJavaKotlin(mod *eval.EvaluatedModule, jarTarget string) ([]string, error) {
	srcs := g.ResolveSrcs(mod)

	var javaSrcs []string
	var ktSrcs []string
	var protoSrcs []string
	var jarSrcs []string
	var srcjarSrcs []string
	var aidlSrcs []string
	var logtagsSrcs []string
	seenJavaSrcs := make(map[string]bool)
	for _, src := range srcs {
		if strings.HasSuffix(src, ".kt") {
			ktSrcs = append(ktSrcs, src)
		} else if strings.HasSuffix(src, ".java") {
			if !seenJavaSrcs[src] {
				seenJavaSrcs[src] = true
				javaSrcs = append(javaSrcs, src)
			}
		} else if strings.HasSuffix(src, ".proto") {
			protoSrcs = append(protoSrcs, src)
		} else if strings.HasSuffix(src, ".srcjar") {
			srcjarSrcs = append(srcjarSrcs, src)
		} else if strings.HasSuffix(src, ".jar") {
			jarSrcs = append(jarSrcs, src)
		} else if strings.HasSuffix(src, ".aidl") {
			aidlSrcs = append(aidlSrcs, src)
		} else if strings.HasSuffix(src, ".logtags") {
			logtagsSrcs = append(logtagsSrcs, src)
		}
	}

	if len(logtagsSrcs) > 0 {
		for _, lt := range logtagsSrcs {
			genJava := filepath.Join(g.opts.OutDir, "gen", mod.Name, "logtags", strings.TrimSuffix(lt, ".logtags")+".java")
			if err := g.nw.Build(ninja.BuildEdge{
				Outputs: []string{genJava},
				Rule:    "logtags_compile",
				Inputs:  []string{lt},
				Variables: map[string]string{
					"top": g.opts.TopDir,
				},
			}); err != nil {
				return nil, err
			}
			javaSrcs = append(javaSrcs, genJava)
		}
	}

	allSrcs := append(append(append(append([]string{}, ktSrcs...), javaSrcs...), srcjarSrcs...), protoSrcs...)
	if len(allSrcs) == 0 && len(aidlSrcs) == 0 {
		staticJars := g.resolveStaticLibsJars(mod)
		if len(staticJars) > 0 {
			jarjarRules := mod.GetString("jarjar_rules")
			outJar := jarTarget
			if jarjarRules != "" {
				outJar = filepath.Join(g.opts.OutDir, "gen", mod.Name, "unjarjared.jar")
			}
			if err := g.nw.Build(ninja.BuildEdge{
				Outputs: []string{outJar},
				Rule:    "merge_zips",
				Inputs:  staticJars,
			}); err != nil {
				return nil, err
			}
			if jarjarRules != "" {
				ruleFile := g.resolveJarjarRulesFile(mod, jarjarRules)
				if err := g.nw.Build(ninja.BuildEdge{
					Outputs:   []string{jarTarget},
					Rule:      "jarjar_process",
					Inputs:    []string{outJar},
					Implicits: []string{ruleFile},
					Variables: map[string]string{
						"rules": ruleFile,
						"top":   g.opts.TopDir,
					},
				}); err != nil {
					return nil, err
				}
			}
			return []string{jarTarget}, nil
		}
		return nil, nil
	}

	cpFlag, cpDeps := g.buildClasspath(mod)
	if len(jarSrcs) > 0 {
		cpDeps = append(cpDeps, jarSrcs...)
		var currentCp []string
		if cpFlag != "" {
			currentCp = strings.Split(strings.TrimPrefix(cpFlag, "-cp "), ":")
		}
		currentCp = append(currentCp, jarSrcs...)
		currentCp = dedup(currentCp)
		cpFlag = "-cp " + strings.Join(currentCp, ":")
	}
	classesDir := filepath.Join(g.opts.OutDir, "classes", mod.Name)

	var rule string
	var cmd string

	encoding := g.opts.JavaEncoding
	if encoding == "" {
		encoding = "UTF-8"
	}
	envPrefix := "export LC_ALL=C.UTF-8 LANG=C.UTF-8 JAVA_TOOL_OPTIONS=\"-Dfile.encoding=UTF-8\" && "

	// System modules support (--system)
	sysModulesArg := ""
	if sm := mod.GetString("system_modules"); sm != "" {
		sysModulesDir := filepath.Join(g.opts.OutDir, "system_modules", sm)
		sysModulesArg = "--system=" + sysModulesDir + " "
		if outs, ok := g.moduleOutputs[sm]; ok {
			cpDeps = append(cpDeps, outs...)
		}
	}

	// Core platform support (--patch-module for libart/jdk internals)
	patchModuleArg := ""
	sdkVer := mod.GetString("sdk_version")
	patchModProp := mod.GetString("patch_module")
	if patchModProp != "" || sdkVer == "" || sdkVer == "core_platform" || sdkVer == "platform_current" {
		targetApi := g.opts.APILevel
		if targetApi <= 0 {
			targetApi = 34
		}
		var patchJars []string
		if artJar := g.resolvePhysicalJar("art-module-lib"); artJar != "" {
			patchJars = append(patchJars, artJar)
			cpDeps = append(cpDeps, artJar)
		} else if coreOj := g.resolvePhysicalJar("core-oj"); coreOj != "" {
			patchJars = append(patchJars, coreOj)
			cpDeps = append(cpDeps, coreOj)
		}
		coreJar := filepath.Join(g.opts.TopDir, "prebuilts/sdk", fmt.Sprintf("%d", targetApi), "module-lib/core-for-system-modules.jar")
		if fi, err := os.Stat(coreJar); err != nil || fi.IsDir() {
			coreJar = filepath.Join(g.opts.TopDir, "prebuilts/sdk/current/module-lib/core-for-system-modules.jar")
		}
		if len(patchJars) == 0 {
			if fi, err := os.Stat(coreJar); err == nil && !fi.IsDir() {
				patchJars = append(patchJars, coreJar)
				cpDeps = append(cpDeps, coreJar)
			}
		}
		artLegacyJar := filepath.Join(g.opts.TopDir, "prebuilts/sdk/current/public/art-coreplatform-legacy.jar")
		if fi, err := os.Stat(artLegacyJar); err == nil && !fi.IsDir() {
			patchJars = append(patchJars, artLegacyJar)
			cpDeps = append(cpDeps, artLegacyJar)
		}
		if len(patchJars) > 0 {
			patchTarget := "java.base"
			if patchModProp != "" {
				patchTarget = patchModProp
			}
			exportSet := map[string]bool{"java.nio": true}
			for _, pj := range patchJars {
				for _, pkg := range collectPatchJarPackages(pj) {
					exportSet[pkg] = true
				}
			}
			var exportPkgs []string
			for pkg := range exportSet {
				exportPkgs = append(exportPkgs, pkg)
			}
			sort.Strings(exportPkgs)
			var addExports []string
			for _, pkg := range exportPkgs {
				addExports = append(addExports, "--add-exports="+patchTarget+"/"+pkg+"=ALL-UNNAMED")
			}
			limitMods := []string{"java.base", "jdk.unsupported"}
			if patchTarget != "java.base" {
				limitMods = append(limitMods, patchTarget)
			}
			patchModuleArg = "--limit-modules " + strings.Join(limitMods, ",") + " --patch-module " + patchTarget + "=" + strings.Join(patchJars, ":") + " " + strings.Join(addExports, " ") + " "
			if coreJar != "" {
				if fi, err := os.Stat(coreJar); err == nil && !fi.IsDir() {
					patchModuleArg += "--patch-module jdk.unsupported=" + coreJar + " --add-exports=jdk.unsupported/sun.misc=ALL-UNNAMED "
				}
			}
		}
	}

	srcjarExtractCmd := ""
	srcjarJavaArg := ""
	if len(srcjarSrcs) > 0 {
		srcjarDir := filepath.Join(classesDir, "srcgen")
		srcjarExtractCmd = "mkdir -p " + srcjarDir + " && for sj in " + strings.Join(srcjarSrcs, " ") + "; do unzip -qo -d " + srcjarDir + " $$sj 2>/dev/null || true; done && find " + srcjarDir + " -name '*.java' > " + classesDir + "/srcjar.list && "
		srcjarJavaArg = " @" + classesDir + "/srcjar.list"
	}

	aidlGenCmd := ""
	aidlJavaArg := ""
	if len(aidlSrcs) > 0 {
		aidlDir := filepath.Join(classesDir, "aidlgen")
		var aidlIncludes []string
		if mod.Dir != "" && mod.Dir != "." {
			aidlIncludes = append(aidlIncludes, "-I"+mod.Dir)
		} else {
			aidlIncludes = append(aidlIncludes, "-I.")
		}
		if aidlMap := mod.GetMap("aidl"); aidlMap != nil {
			for _, key := range []string{"local_include_dirs", "include_dirs", "export_include_dirs"} {
				if incDirs, ok := aidlMap[key].([]interface{}); ok {
					for _, id := range incDirs {
						if s, ok := id.(string); ok {
							if key == "include_dirs" {
								aidlIncludes = append(aidlIncludes, "-I"+s)
							} else if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(s) {
								aidlIncludes = append(aidlIncludes, "-I"+filepath.Join(mod.Dir, s))
							} else {
								aidlIncludes = append(aidlIncludes, "-I"+s)
							}
						}
					}
				}
			}
		}

		// Auto-discover AIDL roots from all aidlSrcs (package path matching)
		pkgRegex := regexp.MustCompile(`package\s+([a-zA-Z0-9_.]+)\s*;`)
		for _, a := range aidlSrcs {
			candPaths := []string{a}
			if g.opts.TopDir != "" && !filepath.IsAbs(a) {
				candPaths = append([]string{filepath.Join(g.opts.TopDir, a)}, candPaths...)
			}
			if g.opts.BpDir != "" && !filepath.IsAbs(a) {
				candPaths = append([]string{filepath.Join(g.opts.BpDir, a)}, candPaths...)
			}
			for _, candP := range candPaths {
				if f, err := os.Open(candP); err == nil {
					buf := make([]byte, 8192)
					n, _ := f.Read(buf)
					_ = f.Close()
					if m := pkgRegex.FindSubmatch(buf[:n]); len(m) > 1 {
						pkgPath := strings.ReplaceAll(string(m[1]), ".", "/")
						dirPath := filepath.Dir(a)
						if strings.HasSuffix(dirPath, pkgPath) {
							root := strings.TrimSuffix(dirPath, pkgPath)
							root = strings.TrimSuffix(root, "/")
							if root != "" {
								aidlIncludes = append(aidlIncludes, "-I"+root)
							}
						}
						break
					}
				}
			}
		}

		nativeBinder := "frameworks/native/aidl/binder"
		if fi, err := os.Stat(filepath.Join(g.opts.TopDir, "prebuilts/target/framework/frameworks/native/aidl/binder")); err == nil && fi.IsDir() {
			nativeBinder = "prebuilts/target/framework/frameworks/native/aidl/binder"
		}
		nativeGui := "frameworks/native/aidl/gui"
		if fi, err := os.Stat(filepath.Join(g.opts.TopDir, "prebuilts/target/framework/frameworks/native/aidl/gui")); err == nil && fi.IsDir() {
			nativeGui = "prebuilts/target/framework/frameworks/native/aidl/gui"
		}

		// Well-known authentic platform AIDL roots
		commonRoots := []string{
			"frameworks/base/core/java",
			"frameworks/base/graphics/java",
			"frameworks/base/keystore/java",
			"frameworks/base/media/java",
			"frameworks/base/media/java/android/media/tv/tunerresourcemanager/aidl",
			"frameworks/base/media/aidl",
			"frameworks/base/mms/java",
			"frameworks/base/telecomm/java",
			"frameworks/base/telephony/java",
			"frameworks/base/wifi/java/src",
			"frameworks/libs/modules-utils/java",
			nativeBinder,
			nativeGui,
			"frameworks/native/libs/incidentcompanion/binder",
			"frameworks/native/cmds/installd/binder",
			"prebuilts/target/system/vold/binder",
			"prebuilts/target/framework/system/vold/binder",
			"prebuilts/target/system/gsid/aidl",
			"prebuilts/target/framework/frameworks/av/camera/aidl",
			"prebuilts/target/framework/frameworks/av/aidl",
			"prebuilts/target/framework/packages/modules/Bluetooth/framework/aidl-export",
			"prebuilts/target/packages/modules/Connectivity/framework/aidl-export",
			"packages/modules/Media/apex/aidl/stable",
			"prebuilts/target/framework/hardware/interfaces/biometrics/common/aidl",
			"prebuilts/target/framework/hardware/interfaces/biometrics/fingerprint/aidl",
			"prebuilts/target/framework/hardware/interfaces/graphics/common/aidl",
			"prebuilts/target/framework/hardware/interfaces/keymaster/aidl",
			"system/hardware/interfaces/media/aidl",
			"prebuilts/target/framework/system/connectivity/wificond/aidl",
		}
		for _, r := range commonRoots {
			cand := r
			if g.opts.TopDir != "" {
				cand = filepath.Join(g.opts.TopDir, r)
			}
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
				aidlIncludes = append(aidlIncludes, "-I"+r)
			}
		}
		aidlIncludes = dedup(aidlIncludes)
		incArgs := strings.Join(aidlIncludes, " ")

		// Emit dedicated aidl_batch_compile edge (xargs-batched via rspfile)
		aidlStamp := filepath.Join(classesDir, "aidlgen.stamp")
		if err := g.nw.Build(ninja.BuildEdge{
			Outputs: []string{aidlStamp},
			Rule:    "aidl_batch_compile",
			Inputs:  aidlSrcs,
			Variables: map[string]string{
				"out_dir": aidlDir,
				"imports": incArgs,
			},
		}); err != nil {
			return nil, err
		}
		cpDeps = append(cpDeps, aidlStamp)

		aidlGenCmd = fmt.Sprintf("find %s -name '*.java' > %s/aidl.list && ", aidlDir, classesDir)
		aidlJavaArg = " @" + classesDir + "/aidl.list"
	}

	protoGenCmd := ""
	protoJavaArg := ""
	if len(protoSrcs) > 0 {
		protoType := "nano"
		if pMap := mod.GetMap("proto"); pMap != nil {
			if t, ok := pMap["type"].(string); ok && t != "" {
				protoType = t
			}
		}

		protoDir := filepath.Join(classesDir, "protogen")
		protoc := g.findPrebuiltHostTool("aprotoc")
		if protoc == "" {
			protoc = g.findPrebuiltHostTool("protoc")
		}
		if protoc == "" {
			protoc = "aprotoc"
		}

		var protoIncludes []string
		if mod.Dir != "" && mod.Dir != "." {
			protoIncludes = append(protoIncludes, "-I"+mod.Dir)
			protoIncludes = append(protoIncludes, "-I"+filepath.Join(mod.Dir, "src"))
		}
		protoIncludes = append(protoIncludes, "-I.")
		if g.opts.TopDir != "" {
			protoIncludes = append(protoIncludes, "-I"+g.opts.TopDir)
			if mod.Dir != "" && mod.Dir != "." {
				protoIncludes = append(protoIncludes, "-I"+filepath.Join(g.opts.TopDir, mod.Dir))
				protoIncludes = append(protoIncludes, "-I"+filepath.Join(g.opts.TopDir, mod.Dir, "src"))
			}
		}
		for _, p := range protoSrcs {
			protoIncludes = append(protoIncludes, "-I"+filepath.Dir(p))
		}
		protoIncludes = dedup(protoIncludes)

		var outFlag string
		if protoType == "nano" {
			plugin := g.findPrebuiltHostTool("protoc-gen-javanano")
			if plugin == "" {
				plugin = "/usr/sbin/protoc-gen-javanano"
			}
			outFlag = "--plugin=protoc-gen-javanano=" + plugin + " --javanano_out=" + protoDir
			if nanoJar := g.resolvePhysicalJar("libprotobuf-java-nano"); nanoJar != "" {
				cpDeps = append(cpDeps, nanoJar)
				var currentCp []string
				if cpFlag != "" {
					currentCp = strings.Split(strings.TrimPrefix(cpFlag, "-cp "), ":")
				}
				currentCp = append(currentCp, nanoJar)
				currentCp = dedup(currentCp)
				cpFlag = "-cp " + strings.Join(currentCp, ":")
			}
		} else if protoType == "lite" {
			outFlag = "--java_out=lite:" + protoDir
			if liteJar := g.resolvePhysicalJar("libprotobuf-java-lite"); liteJar != "" {
				cpDeps = append(cpDeps, liteJar)
				var currentCp []string
				if cpFlag != "" {
					currentCp = strings.Split(strings.TrimPrefix(cpFlag, "-cp "), ":")
				}
				currentCp = append(currentCp, liteJar)
				currentCp = dedup(currentCp)
				cpFlag = "-cp " + strings.Join(currentCp, ":")
			}
		} else {
			outFlag = "--java_out=" + protoDir
		}

		protoGenCmd = "mkdir -p " + protoDir + " && " + protoc + " " + strings.Join(protoIncludes, " ") + " " + outFlag + " " + strings.Join(protoSrcs, " ") + " && find " + protoDir + " -name '*.java' > " + classesDir + "/proto.list && "
		protoJavaArg = " @" + classesDir + "/proto.list"
	}

	var compileTarget string
	staticJars := g.resolveStaticLibsJars(mod)
	jarjarRules := mod.GetString("jarjar_rules")
	if !mod.GetBool("headers_only") && (len(staticJars) > 0 || jarjarRules != "") {
		compileTarget = filepath.Join(classesDir, "classes.jar")
	} else {
		compileTarget = jarTarget
	}

	var rspContent string
	javaFiles := ""
	rspFile := compileTarget + ".rsp"
	if len(javaSrcs) > 0 {
		javaFiles = " @" + rspFile
		rspContent = strings.Join(javaSrcs, " ")
	}

	if mod.GetBool("headers_only") {
		rule = "turbine_compile"

		var bootCpEntries []string
		if g.opts.TopDir != "" {
			artLegacyJar := filepath.Join(g.opts.TopDir, "prebuilts/sdk/current/public/art-coreplatform-legacy.jar")
			if fi, err := os.Stat(artLegacyJar); err == nil && !fi.IsDir() {
				bootCpEntries = append(bootCpEntries, artLegacyJar)
			}
		}
		if len(bootCpEntries) == 0 {
			if cj := g.resolveCurrentAndroidJar(); cj != "" {
				bootCpEntries = append(bootCpEntries, cj)
			}
		}

		var turbineClasspath []string
		if cpFlag != "" {
			turbineClasspath = strings.Split(strings.TrimPrefix(cpFlag, "-cp "), ":")
		}

		var turbineFlags []string
		if len(bootCpEntries) > 0 {
			turbineFlags = append(turbineFlags, "--bootclasspath", strings.Join(bootCpEntries, " "))
		}
		if len(turbineClasspath) > 0 {
			turbineFlags = append(turbineFlags, "--classpath", strings.Join(turbineClasspath, " "))
		}
		if len(srcjarSrcs) > 0 {
			turbineFlags = append(turbineFlags, "--source_jars", strings.Join(srcjarSrcs, " "))
		}
		turbineFlags = append(turbineFlags, "--output", jarTarget)
		var srcArgs []string
		if len(javaSrcs) > 0 {
			srcArgs = append(srcArgs, "@"+rspFile)
		}
		if len(aidlSrcs) > 0 {
			srcArgs = append(srcArgs, "@"+classesDir+"/aidl.list")
		}
		if len(srcArgs) > 0 {
			turbineFlags = append(turbineFlags, "--sources")
			turbineFlags = append(turbineFlags, srcArgs...)
		}

		cmd = envPrefix + "mkdir -p " + classesDir + " && " + aidlGenCmd +
			"$turbine " + strings.Join(turbineFlags, " ")
	} else {
		classOutDir := filepath.Join(classesDir, "classes")
		if len(ktSrcs) > 0 && len(javaSrcs) == 0 && len(srcjarSrcs) == 0 && len(aidlSrcs) == 0 && len(protoSrcs) == 0 {
			// Pure Kotlin compilation
			rule = "kotlinc_compile"
			cpArg := ""
			if cpFlag != "" {
				cpArg = cpFlag + " "
			}
			ktFiles := " @" + rspFile
			rspContent = strings.Join(ktSrcs, " ")
			cmd = envPrefix + "rm -rf " + classOutDir + " && mkdir -p " + classOutDir + " " + classesDir +
				" && $kotlinc -d " + classOutDir + " " + cpArg + ktFiles +
				" && $jar cf " + compileTarget + " -C " + classOutDir + " ."
		} else if len(ktSrcs) > 0 && (len(javaSrcs) > 0 || len(srcjarSrcs) > 0 || len(aidlSrcs) > 0 || len(protoSrcs) > 0) {
			// Mixed Kotlin + Java compilation:
			rule = "kotlin_java_compile"
			cpArg := ""
			if cpFlag != "" {
				cpArg = cpFlag + " "
			}
			javacCp := classOutDir
			if cpFlag != "" {
				javacCp = classOutDir + ":" + strings.TrimPrefix(cpFlag, "-cp ")
			}
			ktFiles := " @" + rspFile
			rspContent = strings.Join(append(ktSrcs, javaSrcs...), " ")
			cmd = envPrefix + "rm -rf " + classOutDir + " && mkdir -p " + classOutDir + " " + classesDir + " && " + aidlGenCmd + srcjarExtractCmd + protoGenCmd +
				"$kotlinc -d " + classOutDir + " " + cpArg + ktFiles +
				" && $javac -encoding " + encoding + " " + sysModulesArg + patchModuleArg + "-d " + classOutDir + " -cp " + javacCp + javaFiles + aidlJavaArg + srcjarJavaArg + protoJavaArg +
				" && $jar cf " + compileTarget + " -C " + classOutDir + " ."
		} else {
			// Pure Java compilation
			rule = "javac_compile"
			cpArg := ""
			if cpFlag != "" {
				cpArg = cpFlag + " "
			}
			cmd = envPrefix + "rm -rf " + classOutDir + " && mkdir -p " + classOutDir + " " + classesDir + " && " + aidlGenCmd + srcjarExtractCmd + protoGenCmd +
				"$javac -encoding " + encoding + " " + sysModulesArg + patchModuleArg + "-d " + classOutDir + " " + cpArg + javaFiles + aidlJavaArg + srcjarJavaArg + protoJavaArg +
				" && $jar cf " + compileTarget + " -C " + classOutDir + " ."
		}

		buildVars := map[string]string{
			"cmd": cmd,
		}
		if rspContent != "" {
			buildVars["rspcontent"] = rspContent
		}

		if err := g.nw.Build(ninja.BuildEdge{
			Outputs:   []string{compileTarget},
			Rule:      rule,
			Inputs:    allSrcs,
			Implicits: cpDeps,
			Variables: buildVars,
		}); err != nil {
			return nil, err
		}

		outJar := jarTarget
		if jarjarRules != "" {
			outJar = filepath.Join(classesDir, "unjarjared.jar")
		}

		if compileTarget != outJar {
			mergeInputs := append([]string{compileTarget}, staticJars...)
			if err := g.nw.Build(ninja.BuildEdge{
				Outputs: []string{outJar},
				Rule:    "merge_zips",
				Inputs:  mergeInputs,
			}); err != nil {
				return nil, err
			}
		}

		if jarjarRules != "" {
			ruleFile := g.resolveJarjarRulesFile(mod, jarjarRules)
			inJar := compileTarget
			if compileTarget != outJar {
				inJar = outJar
			}
			if err := g.nw.Build(ninja.BuildEdge{
				Outputs:   []string{jarTarget},
				Rule:      "jarjar_process",
				Inputs:    []string{inJar},
				Implicits: []string{ruleFile},
				Variables: map[string]string{
					"rules": ruleFile,
					"top":   g.opts.TopDir,
				},
			}); err != nil {
				return nil, err
			}
		}

		return []string{jarTarget}, nil
	}

	buildVars := map[string]string{
		"cmd": cmd,
	}
	if rspContent != "" {
		buildVars["rspcontent"] = rspContent
	}

	err := g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{jarTarget},
		Rule:      rule,
		Inputs:    allSrcs,
		Implicits: cpDeps,
		Variables: buildVars,
	})

	return []string{jarTarget}, err
}

func (g *Generator) generateJavaLibrary(mod *eval.EvaluatedModule) ([]string, error) {
	jarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	outs, err := g.compileJavaKotlin(mod, jarTarget)
	if err != nil {
		return nil, err
	}
	if len(outs) == 0 {
		if physJars := g.resolvePhysicalJars(mod.Name); len(physJars) > 0 {
			if len(physJars) == 1 {
				_ = g.nw.Build(ninja.BuildEdge{
					Outputs: []string{jarTarget},
					Rule:    "copy",
					Inputs:  physJars,
				})
			} else {
				_ = g.nw.Build(ninja.BuildEdge{
					Outputs: []string{jarTarget},
					Rule:    "phony",
					Inputs:  physJars,
				})
				g.emittedPhonies[jarTarget] = true
			}
			return physJars, nil
		}
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(jarTarget)
			return []string{jarTarget}, nil
		}
		return nil, nil
	}
	return outs, nil
}

func (g *Generator) generateJavaImport(mod *eval.EvaluatedModule) ([]string, error) {
	jarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	jars := mod.GetStringList("jars")
	if len(jars) == 0 {
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(jarTarget)
			return []string{jarTarget}, nil
		}
		return nil, nil
	}

	srcJar := jars[0]
	if mod.Dir == "tests_stubs" {
		if physJars := g.resolvePhysicalJars(mod.Name); len(physJars) > 0 {
			srcJar = physJars[0]
		}
	}
	if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(srcJar) {
		srcJar = filepath.Join(mod.Dir, srcJar)
	}

	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{jarTarget},
		Rule:    "copy",
		Inputs:  []string{srcJar},
	})
	return []string{jarTarget}, err
}

func (g *Generator) generateJavaBinary(mod *eval.EvaluatedModule) ([]string, error) {
	jarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	binTarget := filepath.Join(g.binDir(), strings.TrimSuffix(mod.Name, ".jar"))

	jarOutputs, err := g.compileJavaKotlin(mod, jarTarget)
	if err != nil {
		return nil, err
	}
	if len(jarOutputs) == 0 {
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(jarTarget)
		}
	}

	mainClass := mod.GetString("main_class")
	runCmd := fmt.Sprintf(`echo '#!/bin/sh\nexec java -cp "%s" %s "$$@"' > %s && chmod +x %s`,
		jarTarget, mainClass, binTarget, binTarget)

	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{binTarget},
		Rule:    "genrule_cmd",
		Inputs:  jarOutputs,
		Variables: map[string]string{
			"cmd": runCmd,
		},
	}); err != nil {
		return nil, err
	}

	return []string{binTarget, jarTarget}, nil
}

func (g *Generator) generateAndroidApp(mod *eval.EvaluatedModule) ([]string, error) {
	manifest := mod.GetString("manifest")
	if manifest == "" {
		manifest = "AndroidManifest.xml"
	}

	manifestPath := manifest
	if !filepath.IsAbs(manifest) {
		if mod.Dir != "" && mod.Dir != "." {
			manifestPath = filepath.Clean(filepath.Join(mod.Dir, manifest))
		} else if g.opts.BpDir != "" {
			manifestPath = filepath.Join(g.opts.BpDir, manifest)
		}
	}
	g.emitPhonyIfNeeded(manifestPath)

	appDir := filepath.Join(g.opts.OutDir, "apps", mod.Name)
	apkTarget := filepath.Join(g.binDir(), mod.Name+".apk")
	jarTarget := filepath.Join(appDir, mod.Name+".jar")
	genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name)
	srcgenDir := filepath.Join(genDir, "srcgen")
	flataDir := filepath.Join(genDir, "res_flata")
	resApk := filepath.Join(appDir, "resources.apk")
	srcjarTarget := filepath.Join(genDir, mod.Name+".srcjar")

	// 1. Discover resource directories
	rawResDirs := mod.GetStringList("resource_dirs")
	if len(rawResDirs) == 0 {
		checkRes := "res"
		if mod.Dir != "" && mod.Dir != "." {
			checkRes = filepath.Join(mod.Dir, "res")
		}
		if st, err := os.Stat(checkRes); err == nil && st.IsDir() {
			rawResDirs = []string{"res"}
		}
	}
	var resDirs []string
	for _, r := range rawResDirs {
		resolved := r
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(r) {
			resolved = filepath.Clean(filepath.Join(mod.Dir, r))
		}
		resDirs = append(resDirs, resolved)
	}

	// 2. AAPT2 flags and target SDK version
	aaptFlags := mod.GetStringList("aaptflags")
	var extraFlags []string
	extraFlags = append(extraFlags, aaptFlags...)

	minSdk := mod.GetString("min_sdk_version")
	if minSdk == "" {
		minSdk = mod.GetString("sdk_version")
	}
	if minSdk == "" || minSdk == "current" || minSdk == "core_platform" || minSdk == "module_current" || minSdk == "system_current" {
		if g.opts.APILevel > 0 {
			minSdk = fmt.Sprintf("%d", g.opts.APILevel)
		} else {
			minSdk = "34"
		}
	}
	hasMinSdk := false
	for _, f := range aaptFlags {
		if strings.HasPrefix(f, "--min-sdk-version") {
			hasMinSdk = true
			break
		}
	}
	if !hasMinSdk {
		extraFlags = append(extraFlags, "--min-sdk-version "+minSdk)
	}

	androidJar := g.opts.AndroidJarPath
	if androidJar == "" {
		androidJar = g.resolveCurrentAndroidJar()
	}
	if androidJar != "" {
		extraFlags = append(extraFlags, "-I "+androidJar)
	}

	// 3. Emit AAPT2 link & .srcjar generation rule
	var aaptCmd strings.Builder
	aaptCmd.WriteString(fmt.Sprintf("mkdir -p %s %s %s && ", flataDir, srcgenDir, appDir))
	var flataInputs []string
	if len(resDirs) > 0 {
		for i, rd := range resDirs {
			flataFile := filepath.Join(flataDir, fmt.Sprintf("res_%d.flata", i))
			aaptCmd.WriteString(fmt.Sprintf("$aapt2 compile --dir %s -o %s && ", rd, flataFile))
			flataInputs = append(flataInputs, flataFile)
		}
	}

	linkParts := []string{"$aapt2 link", "--manifest " + manifestPath, "-o " + resApk, "--java " + srcgenDir}
	if len(extraFlags) > 0 {
		linkParts = append(linkParts, strings.Join(extraFlags, " "))
	}
	if len(flataInputs) > 0 {
		linkParts = append(linkParts, strings.Join(flataInputs, " "))
	}
	aaptCmd.WriteString(strings.Join(linkParts, " "))
	aaptCmd.WriteString(fmt.Sprintf(" && touch %s/.dummy && $jar cf %s -C %s .", srcgenDir, srcjarTarget, srcgenDir))

	aaptInputs := []string{manifestPath}
	if androidJar != "" {
		aaptInputs = append(aaptInputs, androidJar)
	}
	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{resApk, srcjarTarget},
		Rule:    "genrule_cmd",
		Inputs:  aaptInputs,
		Variables: map[string]string{
			"cmd": aaptCmd.String(),
		},
	}); err != nil {
		return nil, err
	}

	// Register generated srcjar tagged outputs for downstream modules
	g.taggedOutputs[mod.Name+":.aapt.srcjar"] = []string{srcjarTarget}
	g.taggedOutputs[mod.Name+":.srcjar"] = []string{srcjarTarget}

	// 4. Compile Java/Kotlin sources if present
	appSrcs := g.ResolveSrcs(mod)
	var jarOutputs []string
	if len(appSrcs) > 0 {
		origSrcs := mod.GetStringList("srcs")
		mod.Properties["srcs"] = append(origSrcs, srcjarTarget)
		var err error
		jarOutputs, err = g.compileJavaKotlin(mod, jarTarget)
		mod.Properties["srcs"] = origSrcs
		if err != nil {
			return nil, err
		}
	} else {
		staticJars := g.resolveStaticLibsJars(mod)
		if len(staticJars) > 0 {
			if err := g.nw.Build(ninja.BuildEdge{
				Outputs: []string{jarTarget},
				Rule:    "merge_zips",
				Inputs:  staticJars,
			}); err != nil {
				return nil, err
			}
			jarOutputs = []string{jarTarget}
		}
	}
	if len(jarOutputs) == 0 {
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(jarTarget)
		}
	}

	// 5. Final APK target
	finalInputs := []string{resApk}
	if len(jarOutputs) > 0 {
		finalInputs = append(finalInputs, jarOutputs...)
	}

	apkRule := "copy"
	if len(finalInputs) > 1 {
		apkRule = "merge_zips"
	}

	vars := map[string]string{
		"manifest": manifest,
	}
	if g.opts.AndroidJarPath != "" {
		vars["android_jar"] = g.opts.AndroidJarPath
	}

	err := g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{apkTarget},
		Rule:      apkRule,
		Inputs:    finalInputs,
		Variables: vars,
	})
	if err != nil {
		return nil, err
	}

	// 6. Also provide lib/<name>.jar for other modules depending on this app's classes
	libJarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	if len(jarOutputs) > 0 {
		if err := g.nw.Build(ninja.BuildEdge{
			Outputs: []string{libJarTarget},
			Rule:    "copy",
			Inputs:  jarOutputs,
		}); err != nil {
			return nil, err
		}
	} else if g.opts.AllowMissingDeps {
		g.emitPhonyIfNeeded(libJarTarget)
	}

	targets := []string{apkTarget, libJarTarget, srcjarTarget}
	g.moduleOutputs[mod.Name] = targets
	return targets, nil
}

func (g *Generator) generateJavaSdkLibrary(mod *eval.EvaluatedModule) ([]string, error) {
	jarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	outs, err := g.compileJavaKotlin(mod, jarTarget)
	if err != nil {
		return nil, err
	}
	if len(outs) == 0 {
		if physJars := g.resolvePhysicalJars(mod.Name); len(physJars) > 0 {
			if len(physJars) == 1 {
				_ = g.nw.Build(ninja.BuildEdge{
					Outputs: []string{jarTarget},
					Rule:    "copy",
					Inputs:  physJars,
				})
			} else {
				_ = g.nw.Build(ninja.BuildEdge{
					Outputs: []string{jarTarget},
					Rule:    "phony",
					Inputs:  physJars,
				})
				g.emittedPhonies[jarTarget] = true
			}
			outs = []string{jarTarget}
		} else if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(jarTarget)
			outs = []string{jarTarget}
		}
	}

	targets := outs
	subAliases := []string{
		mod.Name + ".impl",
		mod.Name + ".stubs",
		mod.Name + ".stubs.system",
		mod.Name + ".stubs.module_lib",
		mod.Name + ".stubs.test",
	}
	for _, alias := range subAliases {
		aliasTarget := filepath.Join(g.libDir(), alias+".jar")
		if len(outs) > 0 && outs[0] != aliasTarget {
			_ = g.nw.Build(ninja.BuildEdge{
				Outputs: []string{aliasTarget},
				Rule:    "copy",
				Inputs:  []string{jarTarget},
			})
			targets = append(targets, aliasTarget)
		} else if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(aliasTarget)
		}
		g.moduleOutputs[alias] = []string{aliasTarget}
	}

	g.moduleOutputs[mod.Name] = targets
	return targets, nil
}

func (g *Generator) generateAndroidAppImport(mod *eval.EvaluatedModule) ([]string, error) {
	apk := mod.GetString("apk")
	if apk == "" {
		if g.opts.AllowMissingDeps {
			target := filepath.Join(g.binDir(), mod.Name+".apk")
			g.emitPhonyIfNeeded(target)
			return []string{target}, nil
		}
		return nil, nil
	}

	srcApk := apk
	if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(srcApk) {
		srcApk = filepath.Clean(filepath.Join(mod.Dir, srcApk))
	}

	apkTarget := filepath.Join(g.binDir(), mod.Name+".apk")
	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{apkTarget},
		Rule:    "copy",
		Inputs:  []string{srcApk},
	}); err != nil {
		return nil, err
	}

	libJarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{libJarTarget},
		Rule:    "copy",
		Inputs:  []string{srcApk},
	}); err != nil {
		return nil, err
	}

	targets := []string{apkTarget, libJarTarget}

	// Register srcjar if present (e.g. R.java / Manifest.java symbols)
	srcjar := mod.GetString("srcjar")
	if srcjar != "" {
		srcjarPath := srcjar
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(srcjarPath) {
			srcjarPath = filepath.Clean(filepath.Join(mod.Dir, srcjarPath))
		}
		g.taggedOutputs[mod.Name+":.aapt.srcjar"] = []string{srcjarPath}
		g.taggedOutputs[mod.Name+":.srcjar"] = []string{srcjarPath}
		g.taggedOutputs[mod.Name+":.aapt.jar"] = []string{libJarTarget}
	}

	return targets, nil
}

func (g *Generator) generateJavaSdkLibraryImport(mod *eval.EvaluatedModule) ([]string, error) {
	var targets []string

	extractJars := func(subName string) []string {
		if subMap := mod.GetMap(subName); subMap != nil {
			if rawJars, ok := subMap["jars"].([]interface{}); ok {
				var res []string
				for _, r := range rawJars {
					if s, ok := r.(string); ok {
						res = append(res, s)
					}
				}
				return res
			}
		}
		return nil
	}

	moduleLibJars := extractJars("module_lib")
	systemJars := extractJars("system")
	publicJars := extractJars("public")

	bestJars := moduleLibJars
	if len(bestJars) == 0 {
		bestJars = systemJars
	}
	if len(bestJars) == 0 {
		bestJars = publicJars
	}

	registerScope := func(suffix string, jars []string) {
		if len(jars) == 0 {
			return
		}
		srcJar := jars[0]
		if strings.HasPrefix(srcJar, ":") {
			dep := strings.TrimPrefix(srcJar, ":")
			if res := g.resolveJarTargets(dep); len(res) > 0 {
				srcJar = res[0]
			} else if pj := g.resolvePhysicalJar(dep); pj != "" {
				srcJar = pj
			} else if pj := g.resolvePhysicalJar(mod.Name); pj != "" {
				srcJar = pj
			}
		} else if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(srcJar) {
			srcJar = filepath.Clean(filepath.Join(mod.Dir, srcJar))
		}

		if fi, err := os.Stat(srcJar); err != nil || fi.IsDir() {
			if pj := g.resolvePhysicalJar(mod.Name); pj != "" {
				srcJar = pj
			} else if g.opts.AllowMissingDeps {
				g.emitPhonyIfNeeded(srcJar)
			}
		}
		scopeName := mod.Name + suffix
		targetJar := filepath.Join(g.libDir(), scopeName+".jar")
		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{targetJar},
			Rule:    "copy",
			Inputs:  []string{srcJar},
		})
		targets = append(targets, targetJar)
		g.moduleOutputs[scopeName] = []string{targetJar}
	}

	registerScope("", bestJars)
	registerScope(".stubs", publicJars)
	registerScope(".stubs.system", systemJars)
	registerScope(".stubs.module_lib", moduleLibJars)

	return targets, nil
}
