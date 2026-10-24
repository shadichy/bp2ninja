package generator

import (
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// generateSyspropLibrary implements the upstream `sysprop_library` module
// type (build/soong/sysprop): each .sysprop description file is converted by
// the sysprop tool into C++ sources, which are compiled and linked into
// lib<name> (.so/.a) that dependents reference by the module name.
//
// The generator binary is resolved like protoc: pin it per environment via
// $SYSPROP_CLI (platform `sysprop-cli`, or the sysprop_library jar wrapper);
// falls back to `sysprop-cli` on PATH.
func (g *Generator) generateSyspropLibrary(mod *eval.EvaluatedModule) ([]string, error) {
	srcs := g.ResolveSrcs(mod)
	var propFiles []string
	for _, s := range srcs {
		if strings.HasSuffix(s, ".sysprop") {
			propFiles = append(propFiles, s)
		}
	}
	if len(propFiles) == 0 {
		if g.opts.AllowMissingDeps {
			target := filepath.Join(g.opts.OutDir, "gen", mod.Name, mod.Name+".stamp")
			g.emitPhonyIfNeeded(target)
			return []string{target}, nil
		}
		return nil, nil
	}

	genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name)
	objs, err := g.compileSyspropSources(mod, propFiles, genDir)
	if err != nil || len(objs) == 0 {
		return nil, err
	}

	libName := "lib" + strings.TrimPrefix(mod.Name, "lib")
	staticTarget := filepath.Join(g.libDir(), libName+".a")
	sharedTarget := filepath.Join(g.libDir(), libName+".so")

	_ = g.nw.Build(ninja.BuildEdge{
		Outputs: []string{staticTarget},
		Rule:    "archive_static",
		Inputs:  objs,
	})
	ldflags, libs := g.resolveLinkerArgs(mod)
	// Upstream sysprop libraries implicitly link the property runtime.
	libs = append(libs, "-lbase", "-llog")
	_ = g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{sharedTarget},
		Rule:      "link_shared",
		Inputs:    objs,
		Implicits: g.collectLinkImplicits(mod),
		Variables: map[string]string{
			"ldflags": shellJoin(ldflags),
			"libs":    shellJoin(libs),
		},
	})
	return []string{sharedTarget, staticTarget}, nil
}

// compileSyspropSources emits sysprop_cpp codegen edges and compiles the
// generated .cpp files with the module's cflags plus the gen dir on the
// include path.
func (g *Generator) compileSyspropSources(mod *eval.EvaluatedModule, propFiles []string, genDir string) ([]string, error) {
	cflags := append(mod.GetStringList("cflags"), g.opts.ExtraCflags...)
	cflags = stripIncludeFlags(cflags)
	cflags = append(cflags, g.archCodegenFlags()...)
	cflags = append(cflags, g.crossTargetFlags()...)
	cflags = append(cflags, "-fPIC")
	cflags = dedup(cflags)

	objDir := g.objDir(mod.Name + ".sysprop")
	var objs []string
	for _, prop := range propFiles {
		base := strings.TrimSuffix(filepath.Base(prop), ".sysprop")
		genCpp := filepath.Join(genDir, base+".cpp")
		genHdr := filepath.Join(genDir, base+".h")

		if err := g.nw.Build(ninja.BuildEdge{
			Outputs: []string{genCpp, genHdr},
			Rule:    "sysprop_cpp",
			Inputs:  []string{prop},
		}); err != nil {
			return nil, err
		}

		obj := filepath.Join(objDir, base+".o")
		includes := dedup(append(g.resolveIncludeDirs(mod), "-I"+genDir))
		vars := map[string]string{
			"includes": shellJoin(includes),
		}
		if len(cflags) > 0 {
			vars["cflags"] = shellJoin(cflags)
		}
		if err := g.nw.Build(ninja.BuildEdge{
			Outputs:   []string{obj},
			Rule:      "cxx_compile",
			Inputs:    []string{genCpp},
			OrderOnly: []string{genHdr},
			Variables: vars,
		}); err != nil {
			return nil, err
		}
		objs = append(objs, obj)
	}
	return objs, nil
}
