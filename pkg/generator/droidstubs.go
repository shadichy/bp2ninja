package generator

import (
	"fmt"
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// generateDroidstubs handles upstream `droidstubs` and `droiddoc` module types (build/soong/java/droidstubs.go):
// runs metalava to generate API signature files (current.txt) and API stub classes (stubs.srcjar).
func (g *Generator) generateDroidstubs(mod *eval.EvaluatedModule) ([]string, error) {
	srcs := g.ResolveSrcs(mod)
	if len(srcs) == 0 {
		if g.opts.AllowMissingDeps {
			target := filepath.Join(g.opts.OutDir, "gen", mod.Name, mod.Name+".stamp")
			g.emitPhonyIfNeeded(target)
			return []string{target}, nil
		}
		return nil, nil
	}

	apiFilename := mod.GetString("api_filename")
	if apiFilename == "" {
		apiFilename = "current.txt"
	}

	genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name)
	stubsDir := filepath.Join(genDir, "stubs")
	stubsJar := filepath.Join(genDir, "stubs.srcjar")
	apiTxt := filepath.Join(genDir, apiFilename)

	var cmdParts []string
	cmdParts = append(cmdParts, fmt.Sprintf("rm -rf %s", stubsDir))
	cmdParts = append(cmdParts, fmt.Sprintf("mkdir -p %s %s", stubsDir, genDir))

	metalavaArgs := []string{"$metalava"}
	if rawArgs := mod.GetString("args"); rawArgs != "" {
		metalavaArgs = append(metalavaArgs, rawArgs)
	}

	stubPkgs := mod.GetStringList("stub_packages")
	if len(stubPkgs) > 0 {
		metalavaArgs = append(metalavaArgs, "--stub-packages", strings.Join(stubPkgs, ":"))
	}

	metalavaArgs = append(metalavaArgs, "--api", apiTxt)
	metalavaArgs = append(metalavaArgs, "--stubs", stubsDir)

	// Classpath dependencies
	_, cpDeps := g.buildClasspath(mod)
	if len(cpDeps) > 0 {
		metalavaArgs = append(metalavaArgs, "--classpath", strings.Join(cpDeps, ":"))
	}

	metalavaArgs = append(metalavaArgs, "$in")
	cmdParts = append(cmdParts, strings.Join(metalavaArgs, " "))
	cmdParts = append(cmdParts, fmt.Sprintf("$jar cf %s -C %s .", stubsJar, stubsDir))

	fullCmd := strings.Join(cmdParts, " && ")

	inputs := srcs
	inputs = append(inputs, cpDeps...)
	inputs = dedup(inputs)

	outputs := []string{stubsJar, apiTxt}

	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: outputs,
		Rule:    "metalava_stubs",
		Inputs:  inputs,
		Variables: map[string]string{
			"cmd": fullCmd,
		},
	}); err != nil {
		return nil, err
	}

	g.moduleOutputs[mod.Name] = outputs
	g.taggedOutputs[mod.Name+":stubs.srcjar"] = []string{stubsJar}
	g.taggedOutputs[mod.Name+":current.txt"] = []string{apiTxt}
	g.taggedOutputs[mod.Name+":api.txt"] = []string{apiTxt}

	_ = g.nw.Build(ninja.BuildEdge{
		Outputs: []string{mod.Name},
		Rule:    "phony",
		Inputs:  outputs,
	})

	return outputs, nil
}
