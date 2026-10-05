package generator

import (
	"os"
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

func (g *Generator) generateGenrule(mod *eval.EvaluatedModule) ([]string, error) {
	rawCmd := mod.GetString("cmd")
	rawSrcs := mod.GetStringList("srcs")
	outs := mod.GetStringList("out")
	tools := mod.GetStringList("tools")
	toolFiles := mod.GetStringList("tool_files")

	var srcs []string
	for _, s := range rawSrcs {
		for _, ref := range g.resolveReference(s) {
			refPath := ref
			if mod.Dir != "" && mod.Dir != "." && !strings.HasPrefix(ref, ":") && !strings.HasPrefix(ref, "//") && !filepath.IsAbs(ref) {
				refPath = filepath.Clean(filepath.Join(mod.Dir, ref))
			}
			for _, m := range g.expandGlob(refPath) {
				srcs = append(srcs, m)
				if g.opts.AllowMissingDeps {
					fullP := filepath.Join(g.opts.BpDir, m)
					if _, err := os.Stat(fullP); err != nil {
						if _, err2 := os.Stat(m); err2 != nil {
							g.emitPhonyIfNeeded(m)
						}
					}
				}
			}
		}
	}
	for i, tf := range toolFiles {
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(tf) && !strings.HasPrefix(tf, ":") {
			toolFiles[i] = filepath.Clean(filepath.Join(mod.Dir, tf))
		}
	}

	excludeList := mod.GetStringList("exclude_srcs")
	if len(excludeList) > 0 {
		excludeMap := make(map[string]bool)
		for _, ex := range excludeList {
			excludeMap[ex] = true
			excludeMap[filepath.Clean(ex)] = true
			if mod.Dir != "" && mod.Dir != "." {
				excludeMap[filepath.Clean(filepath.Join(mod.Dir, ex))] = true
			}
		}
		var filtered []string
		for _, s := range srcs {
			if !excludeMap[s] && !excludeMap[filepath.Base(s)] {
				filtered = append(filtered, s)
			}
		}
		srcs = filtered
	}

	if len(outs) == 0 {
		outs = mod.GetStringList("outs")
	}
	if len(outs) == 0 {
		outputTmpl := mod.GetString("output")
		if outputTmpl != "" && len(srcs) > 0 {
			for _, s := range srcs {
				base := strings.TrimSuffix(filepath.Base(s), filepath.Ext(s))
				outs = append(outs, strings.ReplaceAll(outputTmpl, "$(in)", base))
			}
		}
	}
	if len(outs) == 0 {
		outs = []string{mod.Name + ".gen"}
	}

	genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name)
	var fullOuts []string
	for _, out := range outs {
		fullOuts = append(fullOuts, filepath.Join(genDir, out))
	}

	// Expand variables in cmd: $(location ...), $(locations ...), $(in), $(out), $(genDir)
	expandedCmd, extraInputs := g.expandGenruleCmd(mod, rawCmd, srcs, outs, fullOuts, tools, toolFiles, genDir)

	// Ensure parent directories for all outputs exist
	dirSet := make(map[string]bool)
	for _, out := range fullOuts {
		dirSet[filepath.Dir(out)] = true
	}
	var dirs []string
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	mkdirCmd := "mkdir -p " + strings.Join(dirs, " ")

	fullCmd := mkdirCmd + " && " + expandedCmd

	err := g.nw.Build(ninja.BuildEdge{
		Outputs:   fullOuts,
		Rule:      "genrule_cmd",
		Inputs:    srcs,
		Implicits: extraInputs,
		Variables: map[string]string{
			"cmd": fullCmd,
		},
	})

	return fullOuts, err
}

func replaceExtension(path, ext string) string {
	ext = strings.TrimPrefix(ext, ".")
	oldExt := filepath.Ext(path)
	base := strings.TrimSuffix(path, oldExt)
	if ext == "" {
		return base
	}
	return base + "." + ext
}

func (g *Generator) generateGensrcs(mod *eval.EvaluatedModule) ([]string, error) {
	rawCmd := mod.GetString("cmd")
	rawSrcs := mod.GetStringList("srcs")
	outputExt := mod.GetString("output_extension")
	tools := mod.GetStringList("tools")
	toolFiles := mod.GetStringList("tool_files")
	data := mod.GetStringList("data")

	var srcs []string
	for _, s := range rawSrcs {
		for _, ref := range g.resolveReference(s) {
			refPath := ref
			if mod.Dir != "" && mod.Dir != "." && !strings.HasPrefix(ref, ":") && !strings.HasPrefix(ref, "//") && !filepath.IsAbs(ref) {
				refPath = filepath.Clean(filepath.Join(mod.Dir, ref))
			}
			for _, m := range g.expandGlob(refPath) {
				srcs = append(srcs, m)
				if g.opts.AllowMissingDeps {
					fullP := filepath.Join(g.opts.BpDir, m)
					if _, err := os.Stat(fullP); err != nil {
						if _, err2 := os.Stat(m); err2 != nil {
							g.emitPhonyIfNeeded(m)
						}
					}
				}
			}
		}
	}

	for i, tf := range toolFiles {
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(tf) && !strings.HasPrefix(tf, ":") {
			toolFiles[i] = filepath.Clean(filepath.Join(mod.Dir, tf))
		}
	}

	for i, d := range data {
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(d) && !strings.HasPrefix(d, ":") {
			data[i] = filepath.Clean(filepath.Join(mod.Dir, d))
		}
	}

	excludeList := mod.GetStringList("exclude_srcs")
	if len(excludeList) > 0 {
		excludeMap := make(map[string]bool)
		for _, ex := range excludeList {
			excludeMap[ex] = true
			excludeMap[filepath.Clean(ex)] = true
			if mod.Dir != "" && mod.Dir != "." {
				excludeMap[filepath.Clean(filepath.Join(mod.Dir, ex))] = true
			}
		}
		var filtered []string
		for _, s := range srcs {
			if !excludeMap[s] && !excludeMap[filepath.Base(s)] {
				filtered = append(filtered, s)
			}
		}
		srcs = filtered
	}

	genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name)
	if len(srcs) == 0 {
		if g.opts.AllowMissingDeps {
			phonyTarget := filepath.Join(genDir, mod.Name+".gen")
			g.emitPhonyIfNeeded(phonyTarget)
			return []string{phonyTarget}, nil
		}
		return nil, nil
	}

	var allOuts []string
	for _, src := range srcs {
		relPath := src
		if mod.Dir != "" && mod.Dir != "." && strings.HasPrefix(relPath, mod.Dir+"/") {
			relPath = strings.TrimPrefix(relPath, mod.Dir+"/")
		}
		outRel := replaceExtension(relPath, outputExt)
		outFile := filepath.Join(genDir, outRel)
		allOuts = append(allOuts, outFile)

		// Expand cmd per source
		expandedCmd, extraInputs := g.expandGenruleCmd(mod, rawCmd, []string{src}, []string{outRel}, []string{outFile}, tools, toolFiles, genDir)
		for _, d := range data {
			extraInputs = append(extraInputs, d)
		}

		outDir := filepath.Dir(outFile)
		fullCmd := "mkdir -p " + outDir + " && " + expandedCmd

		if err := g.nw.Build(ninja.BuildEdge{
			Outputs:   []string{outFile},
			Rule:      "genrule_cmd",
			Inputs:    []string{src},
			Implicits: dedup(extraInputs),
			Variables: map[string]string{
				"cmd": fullCmd,
			},
		}); err != nil {
			return nil, err
		}
	}

	return allOuts, nil
}

func (g *Generator) expandGenruleCmd(
	mod *eval.EvaluatedModule,
	rawCmd string,
	srcs, outs, fullOuts, tools, toolFiles []string,
	genDir string,
) (string, []string) {
	var extraInputs []string
	seenInputs := make(map[string]bool)
	addExtraInput := func(p string) {
		if p != "" && !seenInputs[p] {
			seenInputs[p] = true
			extraInputs = append(extraInputs, p)
			if g.opts.AllowMissingDeps {
				g.emitPhonyIfNeeded(p)
			}
		}
	}

	for _, tool := range tools {
		tPath := g.resolveTool(tool)
		addExtraInput(tPath)
	}
	for _, tf := range toolFiles {
		addExtraInput(tf)
	}

	resolveLabel := func(label string) string {
		label = strings.TrimSpace(label)
		// Check outs
		for _, out := range outs {
			if out == label || filepath.Base(out) == label {
				return filepath.Join(genDir, out)
			}
		}
		// Check srcs
		for _, src := range srcs {
			if src == label || filepath.Base(src) == label {
				return src
			}
			if mod.Dir != "" && filepath.Clean(filepath.Join(mod.Dir, label)) == src {
				return src
			}
		}
		// Check tool_files
		for _, tf := range toolFiles {
			if tf == label || filepath.Base(tf) == label {
				addExtraInput(tf)
				return tf
			}
			if mod.Dir != "" && filepath.Clean(filepath.Join(mod.Dir, label)) == tf {
				addExtraInput(tf)
				return tf
			}
		}
		// Check tools or moduleMap
		tPath := g.resolveTool(label)
		addExtraInput(tPath)
		return tPath
	}

	var sb strings.Builder
	s := rawCmd
	i := 0
	for i < len(s) {
		if s[i] == '$' {
			if i+1 < len(s) && s[i+1] == '$' {
				// Blueprint escaped $: write $$ to Ninja so Ninja passes literal $ to bash
				sb.WriteString("$$")
				i += 2
				continue
			}
			if i+1 < len(s) && s[i+1] == '(' {
				end := strings.IndexByte(s[i+2:], ')')
				if end != -1 {
					fullVar := strings.TrimSpace(s[i+2 : i+2+end])
					switch {
					case fullVar == "location":
						var loc string
						if len(tools) > 0 {
							loc = resolveLabel(tools[0])
						} else if len(toolFiles) > 0 {
							loc = resolveLabel(toolFiles[0])
						} else if len(srcs) > 0 {
							loc = srcs[0]
						}
						sb.WriteString(loc)
					case strings.HasPrefix(fullVar, "location "):
						label := strings.TrimSpace(strings.TrimPrefix(fullVar, "location "))
						sb.WriteString(resolveLabel(label))
					case fullVar == "locations":
						sb.WriteString(strings.Join(srcs, " "))
					case strings.HasPrefix(fullVar, "locations "):
						label := strings.TrimSpace(strings.TrimPrefix(fullVar, "locations "))
						if strings.ContainsAny(label, "*?") {
							var matchedSrcs []string
							for _, src := range srcs {
								if m, _ := filepath.Match(label, src); m {
									matchedSrcs = append(matchedSrcs, src)
									continue
								}
								if m, _ := filepath.Match(label, filepath.Base(src)); m {
									matchedSrcs = append(matchedSrcs, src)
									continue
								}
							}
							if len(matchedSrcs) > 0 {
								sb.WriteString(strings.Join(matchedSrcs, " "))
								break
							}
						}
						if strings.HasPrefix(label, ":") || strings.HasPrefix(label, "//") {
							refs := g.resolveReference(label)
							if len(refs) > 0 {
								for _, r := range refs {
									addExtraInput(r)
								}
								sb.WriteString(strings.Join(refs, " "))
								break
							}
						}
						sb.WriteString(resolveLabel(label))
					case fullVar == "in":
						sb.WriteString(strings.Join(srcs, " "))
					case fullVar == "out":
						sb.WriteString(strings.Join(fullOuts, " "))
					case fullVar == "genDir":
						sb.WriteString(genDir)
					default:
						// Shell subshell or variable e.g. $(dirname foo), emit $$ for Ninja
						sb.WriteString("$$(")
						sb.WriteString(fullVar)
						sb.WriteString(")")
					}
					i += 2 + end + 1
					continue
				}
			}
			// Lone $ - in Ninja must be $$
			sb.WriteString("$$")
			i++
			continue
		}
		sb.WriteByte(s[i])
		i++
	}

	return sb.String(), extraInputs
}

func (g *Generator) generatePrebuilt(mod *eval.EvaluatedModule) ([]string, error) {
	src := mod.GetString("src")
	if src == "" {
		srcs := mod.GetStringList("srcs")
		if len(srcs) > 0 {
			src = srcs[0]
		}
	}

	if src == "" {
		return nil, nil
	}

	resolved := g.resolveReference(src)
	if len(resolved) > 0 {
		src = resolved[0]
	}

	if mod.Dir != "" && mod.Dir != "." && !strings.HasPrefix(src, ":") && !filepath.IsAbs(src) {
		src = filepath.Clean(filepath.Join(mod.Dir, src))
	}

	if strings.ContainsAny(src, "*?[") {
		matches := g.expandGlob(src)
		if len(matches) > 0 {
			src = matches[0]
		}
	}

	g.emitPhonyIfNeeded(src)
	if !filepath.IsAbs(src) && g.opts.BpDir != "" && g.opts.BpDir != "." && !filepath.IsAbs(g.opts.BpDir) {
		inputPath := filepath.Clean(filepath.Join(g.opts.BpDir, src))
		if inputPath != src {
			g.emitPhonyIfNeeded(inputPath)
		}
	}

	filename := mod.GetString("filename")
	if filename == "" {
		filename = mod.GetString("name")
	}
	if filename == "" {
		filename = filepath.Base(src)
	}
	filename = strings.TrimPrefix(filename, ":")

	var rule string = "copy"
	var destDir string
	if mod.Type == "sh_binary" || mod.Type == "sh_binary_host" {
		destDir = g.binDir()
		rule = "copy_executable"
	} else {
		subDir := mod.GetString("sub_dir")
		destDir = filepath.Join(g.opts.OutDir, "etc", subDir)
	}

	target := filepath.Join(destDir, filename)

	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{target},
		Rule:    rule,
		Inputs:  []string{src},
	})

	return []string{target}, err
}

func (g *Generator) generateFallback(mod *eval.EvaluatedModule) ([]string, error) {
	// Generic fallback: compile sources into object files
	objs, err := g.compileCcSources(mod)
	if err != nil || len(objs) == 0 {
		return nil, err
	}

	libBaseName := mod.Name
	if !strings.HasPrefix(libBaseName, "lib") {
		libBaseName = "lib" + libBaseName
	}

	target := filepath.Join(g.libDir(), libBaseName+".a")
	err = g.nw.Build(ninja.BuildEdge{
		Outputs: []string{target},
		Rule:    "archive_static",
		Inputs:  objs,
	})
	if err != nil {
		return nil, err
	}
	targets := []string{target}
	if libBaseName != mod.Name {
		aliasTarget := filepath.Join(g.libDir(), mod.Name+".a")
		if err := g.nw.Build(ninja.BuildEdge{
			Outputs: []string{aliasTarget},
			Rule:    "copy",
			Inputs:  []string{target},
		}); err != nil {
			return nil, err
		}
		targets = append(targets, aliasTarget)
	}
	return targets, nil
}

func (g *Generator) generatePythonBinary(mod *eval.EvaluatedModule) ([]string, error) {
	main := mod.GetString("main")
	if main == "" {
		srcs := mod.GetStringList("srcs")
		for _, s := range srcs {
			if strings.HasSuffix(s, ".py") {
				main = s
				break
			}
		}
		if main == "" && len(srcs) > 0 {
			main = srcs[0]
		}
	}
	if main == "" {
		return nil, nil
	}

	if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(main) && !strings.HasPrefix(main, ":") {
		main = filepath.Clean(filepath.Join(mod.Dir, main))
	}

	g.emitPhonyIfNeeded(main)

	target := filepath.Join(g.binDir(), mod.Name)

	var implicitInputs []string
	for _, s := range mod.GetStringList("srcs") {
		srcPath := s
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(srcPath) && !strings.HasPrefix(srcPath, ":") {
			srcPath = filepath.Clean(filepath.Join(mod.Dir, srcPath))
		}
		if srcPath != main && !strings.ContainsAny(srcPath, "*?[") {
			g.emitPhonyIfNeeded(srcPath)
			implicitInputs = append(implicitInputs, srcPath)
		}
	}

	err := g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{target},
		Rule:      "python_binary",
		Inputs:    []string{main},
		Implicits: implicitInputs,
	})
	if err != nil {
		return nil, err
	}

	return []string{target}, nil
}
