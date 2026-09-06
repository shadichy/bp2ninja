package generator

import (
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

func (g *Generator) generateAndroidApp(mod *eval.EvaluatedModule) ([]string, error) {
	manifest := mod.GetString("manifest")
	if manifest == "" {
		manifest = "AndroidManifest.xml"
	}

	manifestPath := manifest
	if !filepath.IsAbs(manifest) && g.opts.BpDir != "" {
		manifestPath = filepath.Join(g.opts.BpDir, manifest)
	}
	g.emitPhonyIfNeeded(manifestPath)

	appDir := filepath.Join(g.opts.OutDir, "apps", mod.Name)
	apkTarget := filepath.Join(g.binDir(), mod.Name+".apk")

	srcs := g.ResolveSrcs(mod)
	var javaSrcs []string
	for _, src := range srcs {
		if strings.HasSuffix(src, ".java") || strings.HasSuffix(src, ".kt") {
			javaSrcs = append(javaSrcs, src)
		}
	}

	// 1. AAPT2 link rule if resources exist
	resApk := filepath.Join(appDir, "resources.apk")
	vars := map[string]string{
		"manifest": manifest,
	}
	if g.opts.AndroidJarPath != "" {
		vars["android_jar"] = g.opts.AndroidJarPath
	}

	// Resource packaging build edge
	if err := g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{resApk},
		Rule:      "genrule_cmd",
		Inputs:    []string{manifestPath},
		Variables: map[string]string{
			"cmd": "$aapt2 link -I " + g.opts.AndroidJarPath + " --manifest " + manifest + " -o " + resApk,
		},
	}); err != nil {
		return nil, err
	}

	// 2. Final APK target (combining resources and compiled bytecode)
	err := g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{apkTarget},
		Rule:      "copy",
		Inputs:    []string{resApk},
		Variables: vars,
	})

	return []string{apkTarget}, err
}

func (g *Generator) generateJavaLibrary(mod *eval.EvaluatedModule) ([]string, error) {
	jarTarget := filepath.Join(g.libDir(), mod.Name+".jar")
	srcs := g.ResolveSrcs(mod)

	var javaSrcs []string
	for _, src := range srcs {
		if strings.HasSuffix(src, ".java") || strings.HasSuffix(src, ".kt") {
			javaSrcs = append(javaSrcs, src)
		}
	}

	if len(javaSrcs) == 0 {
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(jarTarget)
			return []string{jarTarget}, nil
		}
		return nil, nil
	}

	// Emit javac compilation edge
	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{jarTarget},
		Rule:    "genrule_cmd",
		Inputs:  javaSrcs,
		Variables: map[string]string{
			"cmd": "mkdir -p " + filepath.Join(g.opts.OutDir, "classes", mod.Name) +
				" && javac -d " + filepath.Join(g.opts.OutDir, "classes", mod.Name) + " " + strings.Join(javaSrcs, " ") +
				" && jar cf " + jarTarget + " -C " + filepath.Join(g.opts.OutDir, "classes", mod.Name) + " .",
		},
	})

	return []string{jarTarget}, err
}
