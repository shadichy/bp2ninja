package generator

import (
	"path/filepath"

	"bp2ninja/pkg/eval"
)

// aidlLibraryInfo tracks aidl_library sources, headers, and computed include directories.
type aidlLibraryInfo struct {
	Name        string
	Srcs        []string
	Hdrs        []string
	IncludeDirs []string
	Deps        []string
}

// generateAidlLibrary handles the upstream `aidl_library` module type (build/soong/aidl_library):
// exports modular collections of .aidl interface definitions, headers, and import search paths.
func (g *Generator) generateAidlLibrary(mod *eval.EvaluatedModule) ([]string, error) {
	srcs := g.ResolveSrcs(mod)
	hdrs := mod.GetStringList("hdrs")
	var resolvedHdrs []string
	for _, h := range hdrs {
		hPath := h
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(h) {
			hPath = filepath.Clean(filepath.Join(mod.Dir, h))
		}
		resolvedHdrs = append(resolvedHdrs, hPath)
	}

	incDir := mod.Dir
	if strip := mod.GetString("strip_import_prefix"); strip != "" {
		incDir = filepath.Clean(filepath.Join(mod.Dir, strip))
	}
	if incDir == "" {
		incDir = "."
	}

	info := &aidlLibraryInfo{
		Name:        mod.Name,
		Srcs:        srcs,
		Hdrs:        resolvedHdrs,
		IncludeDirs: []string{incDir},
		Deps:        mod.GetStringList("deps"),
	}
	g.aidlLibraries[mod.Name] = info

	allFiles := append(append([]string{}, srcs...), resolvedHdrs...)
	return allFiles, nil
}
