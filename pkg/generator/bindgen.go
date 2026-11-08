package generator

import (
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// generateRustBindgen handles the `rust_bindgen` and `rust_bindgen_host` module types:
// runs the bindgen CLI tool on a C/C++ header wrapper to generate Rust FFI source bindings.
func (g *Generator) generateRustBindgen(mod *eval.EvaluatedModule) ([]string, error) {
	wrapperSrc := mod.GetString("wrapper_src")
	if wrapperSrc == "" {
		srcs := g.ResolveSrcs(mod)
		for _, s := range srcs {
			if strings.HasSuffix(s, ".h") || strings.HasSuffix(s, ".hpp") {
				wrapperSrc = s
				break
			}
		}
	} else if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(wrapperSrc) {
		wrapperSrc = filepath.Clean(filepath.Join(mod.Dir, wrapperSrc))
	}

	stem := mod.GetString("source_stem")
	if stem == "" {
		stem = "bindings"
	}

	genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name)
	genOut := filepath.Join(genDir, stem+".rs")

	// Collect includes and flags
	includes := g.resolveIncludeDirs(mod)
	cflags := append(mod.GetStringList("cflags"), g.opts.ExtraCflags...)
	cflags = append(cflags, g.archCodegenFlags()...)
	cflags = append(cflags, g.crossTargetFlags()...)
	cflags = append(cflags, includes...)
	cflags = dedup(cflags)

	bindgenFlags := mod.GetStringList("bindgen_flags")

	vars := map[string]string{
		"cflags": shellJoin(cflags),
	}
	if len(bindgenFlags) > 0 {
		escaped := make([]string, 0, len(bindgenFlags))
		for _, f := range bindgenFlags {
			escaped = append(escaped, strings.ReplaceAll(f, "$", "$$"))
		}
		vars["bindgen_flags"] = shellJoin(escaped)
	}

	var inputs []string
	if wrapperSrc != "" {
		inputs = append(inputs, wrapperSrc)
	}

	if err := g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{genOut},
		Rule:      "rust_bindgen",
		Inputs:    inputs,
		Variables: vars,
	}); err != nil {
		return nil, err
	}

	allOutputs := []string{genOut}
	if stem != mod.Name {
		aliasOut := filepath.Join(genDir, mod.Name+".rs")
		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{aliasOut},
			Rule:    "phony",
			Inputs:  []string{genOut},
		})
		allOutputs = append(allOutputs, aliasOut)
	}

	g.moduleOutputs[mod.Name] = allOutputs
	g.taggedOutputs[mod.Name+":"+stem+".rs"] = []string{genOut}
	g.taggedOutputs[mod.Name+":bindings.rs"] = []string{genOut}

	_ = g.nw.Build(ninja.BuildEdge{
		Outputs: []string{mod.Name},
		Rule:    "phony",
		Inputs:  []string{genOut},
	})

	return allOutputs, nil
}
