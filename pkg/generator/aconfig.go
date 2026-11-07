package generator

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

var (
	reAconfigPackage  = regexp.MustCompile(`package:\s*"([^"]+)"`)
	reAconfigFlag     = regexp.MustCompile(`(?s)flag\s*\{([^}]+)\}`)
	reAconfigFlagName = regexp.MustCompile(`name:\s*"([^"]+)"`)
)

func (g *Generator) generateCcAconfigLibrary(mod *eval.EvaluatedModule) ([]string, error) {
	declName := mod.GetString("aconfig_declarations")
	var declMod *eval.EvaluatedModule
	if declName != "" {
		declMod = g.moduleMap[declName]
	}

	pkg := ""
	if declMod != nil {
		pkg = declMod.GetString("package")
	}

	var flagNames []string
	seenFlags := make(map[string]bool)

	// Collect sources from declMod
	var aconfigFiles []string
	if declMod != nil {
		aconfigFiles = g.ResolveSrcs(declMod)
	}

	// Try reading aconfig files to get package and flags
	for _, f := range aconfigFiles {
		candidatePaths := []string{
			filepath.Join(g.opts.BpDir, f),
			f,
		}
		if g.opts.TopDir != "" {
			candidatePaths = append(candidatePaths, filepath.Join(g.opts.TopDir, f))
		}
		for _, p := range candidatePaths {
			data, err := os.ReadFile(p)
			if err == nil {
				content := string(data)
				if pkg == "" {
					if m := reAconfigPackage.FindStringSubmatch(content); len(m) > 1 {
						pkg = m[1]
					}
				}
				for _, flagBlock := range reAconfigFlag.FindAllStringSubmatch(content, -1) {
					if len(flagBlock) > 1 {
						if nm := reAconfigFlagName.FindStringSubmatch(flagBlock[1]); len(nm) > 1 {
							flagName := nm[1]
							if !seenFlags[flagName] {
								seenFlags[flagName] = true
								flagNames = append(flagNames, flagName)
							}
						}
					}
				}
				break
			}
		}
	}

	// Fallback for package name if still empty
	if pkg == "" {
		clean := strings.TrimSuffix(mod.Name, "_cc_lib")
		clean = strings.TrimSuffix(clean, "_c_lib")
		clean = strings.TrimSuffix(clean, "_lib")
		clean = strings.TrimSuffix(clean, "_flags")
		pkg = "android." + strings.ReplaceAll(clean, "_", ".")
	}

	headerBase := strings.ReplaceAll(pkg, ".", "_")
	cppNamespace := strings.ReplaceAll(pkg, ".", "::")
	macroPrefix := strings.ToUpper(headerBase)

	// Generate Header Content
	var hdrBuf bytes.Buffer
	hdrBuf.WriteString("#pragma once\n\n")

	hdrBuf.WriteString(fmt.Sprintf("#ifndef %s\n#define %s(FLAG) %s_##FLAG\n#endif\n", macroPrefix, macroPrefix, macroPrefix))
	for _, fn := range flagNames {
		flagMacro := strings.ToUpper(fn)
		hdrBuf.WriteString(fmt.Sprintf("#ifndef %s_%s\n#define %s_%s false\n#endif\n", macroPrefix, flagMacro, macroPrefix, flagMacro))
	}
	hdrBuf.WriteString("\n")

	hdrBuf.WriteString("#ifdef __cplusplus\n")
	hdrBuf.WriteString("#include <memory>\n\n")
	hdrBuf.WriteString(fmt.Sprintf("namespace %s {\n\n", cppNamespace))
	hdrBuf.WriteString("class flag_provider_interface {\n")
	hdrBuf.WriteString("public:\n")
	hdrBuf.WriteString("    virtual ~flag_provider_interface() = default;\n")
	for _, fn := range flagNames {
		hdrBuf.WriteString(fmt.Sprintf("    virtual bool %s() = 0;\n", fn))
	}
	hdrBuf.WriteString("};\n\n")
	hdrBuf.WriteString("extern std::unique_ptr<flag_provider_interface> provider_;\n\n")
	for _, fn := range flagNames {
		hdrBuf.WriteString(fmt.Sprintf("inline bool %s() {\n    return false;\n}\n", fn))
	}
	hdrBuf.WriteString(fmt.Sprintf("\n} // namespace %s\n\n", cppNamespace))
	hdrBuf.WriteString("extern \"C\" {\n")
	hdrBuf.WriteString("#endif // __cplusplus\n\n")
	for _, fn := range flagNames {
		hdrBuf.WriteString(fmt.Sprintf("bool %s_%s();\n", headerBase, fn))
	}
	hdrBuf.WriteString("\n#ifdef __cplusplus\n")
	hdrBuf.WriteString("} // extern \"C\"\n")
	hdrBuf.WriteString("#endif\n")

	// Generate Source Content
	var srcBuf bytes.Buffer
	srcBuf.WriteString(fmt.Sprintf("#include \"%s.h\"\n\n", headerBase))
	srcBuf.WriteString(fmt.Sprintf("namespace %s {\n\n", cppNamespace))
	srcBuf.WriteString("class flag_provider : public flag_provider_interface {\n")
	srcBuf.WriteString("public:\n")
	srcBuf.WriteString("    virtual ~flag_provider() = default;\n")
	for _, fn := range flagNames {
		srcBuf.WriteString(fmt.Sprintf("    virtual bool %s() override {\n        return false;\n    }\n", fn))
	}
	srcBuf.WriteString("};\n\n")
	srcBuf.WriteString("std::unique_ptr<flag_provider_interface> provider_ =\n    std::make_unique<flag_provider>();\n\n")
	srcBuf.WriteString(fmt.Sprintf("} // namespace %s\n\n", cppNamespace))
	srcBuf.WriteString("extern \"C\" {\n")
	for _, fn := range flagNames {
		srcBuf.WriteString(fmt.Sprintf("bool %s_%s() {\n    return false;\n}\n", headerBase, fn))
	}
	srcBuf.WriteString("}\n")

	// Target file locations
	includeDir := filepath.Join(g.opts.OutDir, "gen", mod.Name, "include")
	genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name)
	headerPath := filepath.Join(includeDir, headerBase+".h")
	sourcePath := filepath.Join(genDir, headerBase+".cc")
	objPath := filepath.Join(g.objDir(mod.Name), headerBase+".o")

	// Write files to disk during generation
	if err := os.MkdirAll(includeDir, 0755); err != nil {
		return nil, fmt.Errorf("failed creating include dir %s: %w", includeDir, err)
	}
	if err := os.WriteFile(headerPath, hdrBuf.Bytes(), 0644); err != nil {
		return nil, fmt.Errorf("failed writing header %s: %w", headerPath, err)
	}
	if err := os.MkdirAll(genDir, 0755); err != nil {
		return nil, fmt.Errorf("failed creating gen dir %s: %w", genDir, err)
	}
	if err := os.WriteFile(sourcePath, srcBuf.Bytes(), 0644); err != nil {
		return nil, fmt.Errorf("failed writing source %s: %w", sourcePath, err)
	}

	g.aconfigHeaders[mod.Name] = []string{headerPath}

	// Ninja rule for compiling C++ source
	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{objPath},
		Rule:    "cxx_compile",
		Inputs:  []string{sourcePath},
		Variables: map[string]string{
			"cflags":   "-std=c++17 -fPIC",
			"includes": "-I" + includeDir,
		},
	}); err != nil {
		return nil, err
	}

	// Archive static library
	libBaseName := mod.Name
	if !strings.HasPrefix(libBaseName, "lib") {
		libBaseName = "lib" + libBaseName
	}
	staticTarget := filepath.Join(g.libDir(), libBaseName+".a")

	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{staticTarget},
		Rule:    "archive_static",
		Inputs:  []string{objPath},
	}); err != nil {
		return nil, err
	}

	targets := []string{staticTarget}
	if libBaseName != mod.Name {
		aliasTarget := filepath.Join(g.libDir(), mod.Name+".a")
		if err := g.nw.Build(ninja.BuildEdge{
			Outputs: []string{aliasTarget},
			Rule:    "copy",
			Inputs:  []string{staticTarget},
		}); err != nil {
			return nil, err
		}
		targets = append(targets, aliasTarget)
	}

	return targets, nil
}

func (g *Generator) generateJavaAconfigLibrary(mod *eval.EvaluatedModule) ([]string, error) {
	if outs, ok := g.taggedOutputs[mod.Name+":.srcjars"]; ok && len(outs) > 0 {
		return outs, nil
	}

	declName := mod.GetString("aconfig_declarations")
	var declMod *eval.EvaluatedModule
	if declName != "" {
		declMod = g.moduleMap[declName]
	}

	pkg := ""
	if declMod != nil {
		pkg = declMod.GetString("package")
	}

	var aconfigFiles []string
	if declMod != nil {
		aconfigFiles = g.ResolveSrcs(declMod)
	}

	jarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	if len(aconfigFiles) == 0 {
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(jarTarget)
		}
		g.moduleOutputs[mod.Name] = []string{jarTarget}
		g.taggedOutputs[mod.Name+":.srcjars"] = []string{jarTarget}
		g.taggedOutputs[mod.Name+":.srcjar"] = []string{jarTarget}
		return []string{jarTarget}, nil
	}

	// Try reading aconfig files to get package if empty
	if pkg == "" {
		for _, f := range aconfigFiles {
			candidatePaths := []string{
				filepath.Join(g.opts.BpDir, f),
				f,
			}
			if g.opts.TopDir != "" {
				candidatePaths = append(candidatePaths, filepath.Join(g.opts.TopDir, f))
			}
			for _, p := range candidatePaths {
				if data, err := os.ReadFile(p); err == nil {
					if m := reAconfigPackage.FindStringSubmatch(string(data)); len(m) > 1 {
						pkg = m[1]
						break
					}
				}
			}
			if pkg != "" {
				break
			}
		}
	}

	outGenDir := filepath.Join(g.opts.OutDir, "gen", mod.Name)
	cacheFile := filepath.Join(outGenDir, mod.Name+".cache")
	srcGenDir := filepath.Join(outGenDir, "src")
	srcjar := filepath.Join(outGenDir, mod.Name+".srcjar")

	var declFlags []string
	for _, f := range aconfigFiles {
		declFlags = append(declFlags, "--declarations "+f)
	}

	vars := map[string]string{
		"pkg":        pkg,
		"cache_dir":  outGenDir,
		"out_cache":  cacheFile,
		"gen_dir":    srcGenDir,
		"decl_flags": strings.Join(declFlags, " "),
	}

	if err := g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{srcjar},
		Rule:      "aconfig_java",
		Inputs:    aconfigFiles,
		Variables: vars,
	}); err != nil {
		return nil, err
	}

	// Compile the generated Java classes into jarTarget
	classesDir := filepath.Join(g.opts.OutDir, "classes", mod.Name, "classes")
	var cpEntries []string
	var cpDeps []string

	// aconfig annotations
	annJar := filepath.Join(g.libDir(), "aconfig-annotations-lib.jar")
	if g.opts.AllowMissingDeps {
		g.emitPhonyIfNeeded(annJar)
	}
	cpEntries = append(cpEntries, annJar)
	cpDeps = append(cpDeps, annJar)

	// unsupportedappusage
	unsuppJar := filepath.Join(g.libDir(), "unsupportedappusage.jar")
	if g.opts.AllowMissingDeps {
		g.emitPhonyIfNeeded(unsuppJar)
	}
	cpEntries = append(cpEntries, unsuppJar)
	cpDeps = append(cpDeps, unsuppJar)

	// Platform classpath or Android Jar
	if len(g.opts.PlatformClasspath) > 0 {
		for _, pj := range g.opts.PlatformClasspath {
			if pj != "" {
				cpEntries = append(cpEntries, pj)
				cpDeps = append(cpDeps, pj)
			}
		}
	} else if jar := g.resolveCurrentAndroidJar(); jar != "" {
		cpEntries = append(cpEntries, jar)
		cpDeps = append(cpDeps, jar)
	}

	compileCmd := fmt.Sprintf(
		"export LC_ALL=C.UTF-8 LANG=C.UTF-8 JAVA_TOOL_OPTIONS=\"-Dfile.encoding=UTF-8\" && "+
			"rm -rf %s && mkdir -p %s && "+
			"find %s -name '*.java' > %s/sources.txt && "+
			"$javac -encoding UTF-8 -source 17 -target 17 -Xlint:-options -d %s -cp %s @%s/sources.txt && "+
			"$jar cf %s -C %s .",
		classesDir, classesDir,
		srcGenDir, classesDir,
		classesDir, strings.Join(cpEntries, ":"), classesDir,
		jarTarget, classesDir,
	)

	compileVars := map[string]string{
		"cmd": compileCmd,
	}

	if err := g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{jarTarget},
		Rule:      "aconfig_compile_java",
		Inputs:    []string{srcjar},
		Implicits: cpDeps,
		Variables: compileVars,
	}); err != nil {
		return nil, err
	}

	outs := []string{srcjar, jarTarget}
	g.moduleOutputs[mod.Name] = outs
	g.taggedOutputs[mod.Name+":.srcjars"] = []string{srcjar}
	g.taggedOutputs[mod.Name+":.srcjar"] = []string{srcjar}
	return outs, nil
}

func (g *Generator) generateAconfigDeclarationsGroup(mod *eval.EvaluatedModule) ([]string, error) {
	if outs, ok := g.moduleOutputs[mod.Name]; ok && len(outs) > 0 {
		return outs, nil
	}
	var allSrcjars []string
	for _, lib := range mod.GetStringList("java_aconfig_libraries") {
		if outs, ok := g.taggedOutputs[lib+":.srcjars"]; ok && len(outs) > 0 {
			allSrcjars = append(allSrcjars, outs...)
		} else if libMod, ok := g.moduleMap[lib]; ok && libMod.Type == "java_aconfig_library" {
			outs, _ := g.generateJavaAconfigLibrary(libMod)
			if len(outs) > 0 {
				allSrcjars = append(allSrcjars, outs[0])
			}
		} else if outs, ok := g.moduleOutputs[lib]; ok && len(outs) > 0 {
			allSrcjars = append(allSrcjars, outs[0])
		}
	}
	for _, grp := range mod.GetStringList("aconfig_declarations_groups") {
		if outs, ok := g.taggedOutputs[grp+":.srcjars"]; ok && len(outs) > 0 {
			allSrcjars = append(allSrcjars, outs...)
		} else if grpMod, ok := g.moduleMap[grp]; ok && grpMod.Type == "aconfig_declarations_group" {
			outs, _ := g.generateAconfigDeclarationsGroup(grpMod)
			allSrcjars = append(allSrcjars, outs...)
		} else if outs, ok := g.moduleOutputs[grp]; ok && len(outs) > 0 {
			allSrcjars = append(allSrcjars, outs...)
		}
	}
	allSrcjars = dedup(allSrcjars)
	g.moduleOutputs[mod.Name] = allSrcjars
	g.taggedOutputs[mod.Name+":.srcjars"] = allSrcjars
	g.taggedOutputs[mod.Name+":.srcjar"] = allSrcjars
	return allSrcjars, nil
}
