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
		for _, s := range declMod.GetStringList("srcs") {
			fullPattern := s
			if declMod.Dir != "" && declMod.Dir != "." && !filepath.IsAbs(s) {
				fullPattern = filepath.Join(declMod.Dir, s)
			}
			for _, m := range g.expandGlob(fullPattern) {
				aconfigFiles = append(aconfigFiles, m)
			}
		}
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
