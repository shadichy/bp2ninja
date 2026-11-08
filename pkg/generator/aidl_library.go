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

	for _, inc := range mod.GetStringList("include_dirs") {
		if inc != "" {
			if g.opts.TopDir != "" && g.opts.TopDir != "." && !filepath.IsAbs(inc) {
				info.IncludeDirs = append(info.IncludeDirs, filepath.Clean(filepath.Join(g.opts.TopDir, inc)))
			} else {
				info.IncludeDirs = append(info.IncludeDirs, inc)
			}
		}
	}
	for _, inc := range mod.GetStringList("local_include_dirs") {
		if inc != "" {
			if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(inc) {
				info.IncludeDirs = append(info.IncludeDirs, filepath.Clean(filepath.Join(mod.Dir, inc)))
			} else {
				info.IncludeDirs = append(info.IncludeDirs, inc)
			}
		}
	}
	info.IncludeDirs = dedup(info.IncludeDirs)

	g.aidlLibraries[mod.Name] = info

	allFiles := append(append([]string{}, srcs...), resolvedHdrs...)
	return allFiles, nil
}

func (g *Generator) resolveAidlLibrary(name string) *aidlLibraryInfo {
	if info, ok := g.aidlLibraries[name]; ok {
		return info
	}
	if mod, ok := g.moduleMap[name]; ok && mod.Type == "aidl_library" {
		if _, err := g.generateAidlLibrary(mod); err == nil {
			return g.aidlLibraries[name]
		}
	}
	return nil
}

func (g *Generator) collectAidlLibraryClosure(name string, visited map[string]bool) (srcs, hdrs, incDirs []string) {
	if visited[name] {
		return nil, nil, nil
	}
	visited[name] = true
	info := g.resolveAidlLibrary(name)
	if info == nil {
		return nil, nil, nil
	}
	srcs = append(srcs, info.Srcs...)
	hdrs = append(hdrs, info.Hdrs...)
	incDirs = append(incDirs, info.IncludeDirs...)
	for _, dep := range info.Deps {
		subSrcs, subHdrs, subIncs := g.collectAidlLibraryClosure(dep, visited)
		srcs = append(srcs, subSrcs...)
		hdrs = append(hdrs, subHdrs...)
		incDirs = append(incDirs, subIncs...)
	}
	return dedup(srcs), dedup(hdrs), dedup(incDirs)
}
