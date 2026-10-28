package generator

import (
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// generateXsdConfig handles the upstream `xsd_config` module type (build/soong/xsdc):
// translates XML schema (.xsd) files into C++ parsers/enums and Java .srcjar classes via xsdc.
func (g *Generator) generateXsdConfig(mod *eval.EvaluatedModule) ([]string, error) {
	srcs := g.ResolveSrcs(mod)
	if len(srcs) == 0 {
		if g.opts.AllowMissingDeps {
			target := filepath.Join(g.opts.OutDir, "gen", mod.Name, mod.Name+".stamp")
			g.emitPhonyIfNeeded(target)
			return []string{target}, nil
		}
		return nil, nil
	}

	schema := srcs[0]
	pkgName := mod.GetString("package_name")
	if pkgName == "" {
		pkgName = mod.Name
	}
	filenameStem := strings.ReplaceAll(pkgName, ".", "_")

	genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name)
	cppDir := filepath.Join(genDir, "cpp")
	includeDir := filepath.Join(cppDir, "include")
	javaDir := filepath.Join(genDir, "java")
	srcjar := filepath.Join(javaDir, filenameStem+"_xsdcgen.srcjar")
	copiedXsd := filepath.Join(genDir, mod.Name+".xsd")

	var args []string
	if mod.GetBool("gen_writer") {
		args = append(args, "-w")
	}
	if mod.GetBool("nullability") {
		args = append(args, "-n")
	}
	if mod.GetBool("gen_has") {
		args = append(args, "-g")
	}
	if mod.GetBool("enums_only") {
		args = append(args, "-e")
	}
	if mod.GetBool("parser_only") {
		args = append(args, "-x")
	}
	if mod.GetBool("boolean_getter") {
		args = append(args, "-b")
	}
	if mod.GetBool("tinyxml") {
		args = append(args, "-t")
	}
	for _, root := range mod.GetStringList("root_elements") {
		args = append(args, "-r", root)
	}

	var implicits []string
	for _, inc := range mod.GetStringList("include_files") {
		incPath := inc
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(inc) {
			incPath = filepath.Clean(filepath.Join(mod.Dir, inc))
		}
		implicits = append(implicits, incPath)
	}

	var cppFiles []string
	var hFiles []string

	enumsOnly := mod.GetBool("enums_only")
	parserOnly := mod.GetBool("parser_only")

	if enumsOnly {
		cppFiles = []string{filepath.Join(cppDir, filenameStem+"_enums.cpp")}
		hFiles = []string{filepath.Join(includeDir, filenameStem+"_enums.h")}
	} else if parserOnly {
		cppFiles = []string{filepath.Join(cppDir, filenameStem+".cpp")}
		hFiles = []string{filepath.Join(includeDir, filenameStem+".h")}
	} else {
		cppFiles = []string{
			filepath.Join(cppDir, filenameStem+".cpp"),
			filepath.Join(cppDir, filenameStem+"_enums.cpp"),
		}
		hFiles = []string{
			filepath.Join(includeDir, filenameStem+".h"),
			filepath.Join(includeDir, filenameStem+"_enums.h"),
		}
	}

	// 1. Emit C++ generation rule
	cppVars := map[string]string{
		"package_name": pkgName,
		"out_dir":      cppDir,
	}
	if len(args) > 0 {
		cppVars["args"] = strings.Join(args, " ")
	}
	if err := g.nw.Build(ninja.BuildEdge{
		Outputs:   append(append([]string{}, cppFiles...), hFiles...),
		Rule:      "xsdc_cpp",
		Inputs:    []string{schema},
		Implicits: implicits,
		Variables: cppVars,
	}); err != nil {
		return nil, err
	}

	// 2. Emit Java generation and srcjar packaging rule
	javaVars := map[string]string{
		"package_name": pkgName,
		"out_dir":      javaDir,
	}
	if len(args) > 0 {
		javaVars["args"] = strings.Join(args, " ")
	}
	if err := g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{srcjar},
		Rule:      "xsdc_java",
		Inputs:    []string{schema},
		Implicits: implicits,
		Variables: javaVars,
	}); err != nil {
		return nil, err
	}

	// 3. Emit schema copy rule for data: [":<name>"] test dependencies
	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{copiedXsd},
		Rule:    "xsd_copy",
		Inputs:  []string{schema},
	}); err != nil {
		return nil, err
	}

	// Register tagged references
	g.taggedOutputs[mod.Name+":cpp"] = cppFiles
	g.taggedOutputs[mod.Name+":h"] = hFiles
	g.taggedOutputs[mod.Name+":java"] = []string{srcjar}

	allOuts := append(append(append([]string{}, cppFiles...), hFiles...), srcjar, copiedXsd)
	return allOuts, nil
}
