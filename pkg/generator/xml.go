package generator

import (
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// generatePrebuiltEtcXml handles the upstream `prebuilt_etc_xml` module type (build/soong/xml):
// stages XML configuration files to etc/<sub_dir> while optionally validating against XSD or DTD schemas.
func (g *Generator) generatePrebuiltEtcXml(mod *eval.EvaluatedModule) ([]string, error) {
	src := mod.GetString("src")
	if src == "" {
		srcs := mod.GetStringList("srcs")
		if len(srcs) > 0 {
			src = srcs[0]
		}
	}
	if src == "" {
		if g.opts.AllowMissingDeps {
			target := filepath.Join(g.opts.OutDir, "etc", mod.Name+".xml")
			g.emitPhonyIfNeeded(target)
			return []string{target}, nil
		}
		return nil, nil
	}

	srcPath := src
	if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(src) {
		srcPath = filepath.Clean(filepath.Join(mod.Dir, src))
	}

	filename := mod.GetString("filename")
	if filename == "" {
		filename = filepath.Base(src)
	}

	subDir := mod.GetString("sub_dir")
	outDir := filepath.Join(g.opts.OutDir, "etc")
	if subDir != "" {
		outDir = filepath.Join(outDir, subDir)
	}
	outPath := filepath.Join(outDir, filename)

	schema := mod.GetString("schema")
	if schema == "" {
		schema = mod.GetString("dtd")
	}
	if schema != "" {
		schemaPath := schema
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(schema) {
			schemaPath = filepath.Clean(filepath.Join(mod.Dir, schema))
		}

		if strings.HasSuffix(schema, ".dtd") {
			err := g.nw.Build(ninja.BuildEdge{
				Outputs:   []string{outPath},
				Rule:      "xmllint_dtd",
				Inputs:    []string{srcPath},
				Implicits: []string{schemaPath},
				Variables: map[string]string{
					"dtd": schemaPath,
				},
			})
			return []string{outPath}, err
		}

		// Default to XSD schema validation
		err := g.nw.Build(ninja.BuildEdge{
			Outputs:   []string{outPath},
			Rule:      "xmllint_xsd",
			Inputs:    []string{srcPath},
			Implicits: []string{schemaPath},
			Variables: map[string]string{
				"schema": schemaPath,
			},
		})
		return []string{outPath}, err
	}

	// No schema: standard copy
	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{outPath},
		Rule:    "copy",
		Inputs:  []string{srcPath},
	})
	return []string{outPath}, err
}
