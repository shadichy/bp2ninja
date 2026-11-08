package generator

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoveredBp represents an Android.bp file discovered in a directory tree.
type DiscoveredBp struct {
	Path   string
	RelDir string
	Depth  int
}

// DiscoverBpFiles searches rootDir recursively for all Android.bp files without caching.
// It skips hidden directories (names starting with '.'), "out", and "node_modules".
// Discovered files are sorted primarily by directory depth (shallower directories first)
// and secondarily by relative directory name alphabetically.
func DiscoverBpFiles(rootDir string) ([]DiscoveredBp, error) {
	bps, _, err := DiscoverBpFilesWithCache(rootDir, false, "")
	return bps, err
}

// DiscoverBpFilesWithCache searches rootDir recursively for all Android.bp files.
// If useCache is true, any subdirectory containing a valid and fresh bp2ninja build.ninja
// (other than outNinja) is added to cachedSubdirs and skipped entirely from recursive
// descent, bypassing redundant parsing of that directory and all of its descendants.
func DiscoverBpFilesWithCache(rootDir string, useCache bool, outNinja string) ([]DiscoveredBp, []string, error) {
	var discovered []DiscoveredBp
	var cachedSubdirs []string

	absOutNinja := ""
	if outNinja != "" {
		if abs, err := filepath.Abs(outNinja); err == nil {
			absOutNinja = abs
		}
	}

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := info.Name()
		if info.IsDir() {
			if path != rootDir {
				if strings.HasPrefix(name, ".") || name == "out" || name == "node_modules" {
					return filepath.SkipDir
				}
				if useCache {
					candNinja := filepath.Join(path, "build.ninja")
					absCand, err := filepath.Abs(candNinja)
					if err == nil && absCand != absOutNinja {
						if fresh, _ := IsCachedNinjaFresh(path); fresh {
							cachedSubdirs = append(cachedSubdirs, path)
							return filepath.SkipDir
						}
					}
				}
			}
			return nil
		}
		if name == "Android.bp" {
			relPath, err := filepath.Rel(rootDir, path)
			if err != nil {
				return nil
			}
			relDir := filepath.Dir(relPath)
			if relDir == "." {
				relDir = ""
			}
			depth := 0
			if relDir != "" {
				depth = strings.Count(relDir, string(filepath.Separator)) + 1
			}
			discovered = append(discovered, DiscoveredBp{
				Path:   path,
				RelDir: relDir,
				Depth:  depth,
			})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	sort.Slice(discovered, func(i, j int) bool {
		if discovered[i].Depth != discovered[j].Depth {
			return discovered[i].Depth < discovered[j].Depth
		}
		return discovered[i].RelDir < discovered[j].RelDir
	})

	sort.Strings(cachedSubdirs)
	return discovered, cachedSubdirs, nil
}
