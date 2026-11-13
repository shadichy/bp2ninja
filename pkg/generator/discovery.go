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
	bps, _, err := DiscoverBpFilesWithCache(rootDir, false, "", nil)
	return bps, err
}

// DiscoverBpFilesWithCache searches rootDir recursively for all Android.bp files.
// If useCache is true, any subdirectory containing a valid and fresh bp2ninja build.ninja
// (other than outNinja) is added to cachedSubdirs and skipped entirely from recursive
// descent, bypassing redundant parsing of that directory and all of its descendants.
// excludeDirs is an optional list of directory names or relative paths to skip during
// recursive discovery. A plain name (no path separator) matches any directory with that
// basename; a relative path is matched against the path relative to rootDir.
func DiscoverBpFilesWithCache(rootDir string, useCache bool, outNinja string, excludeDirs []string) ([]DiscoveredBp, []string, error) {
	var discovered []DiscoveredBp
	var cachedSubdirs []string

	absOutNinja := ""
	if outNinja != "" {
		if abs, err := filepath.Abs(outNinja); err == nil {
			absOutNinja = abs
		}
	}

	// Build exclude sets: plain basenames vs relative path prefixes
	excludeNames := make(map[string]bool)
	var excludePaths []string
	for _, ex := range excludeDirs {
		ex = filepath.Clean(ex)
		if strings.Contains(ex, string(filepath.Separator)) {
			excludePaths = append(excludePaths, ex)
		} else {
			excludeNames[ex] = true
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
				// Check user-specified exclude-dir by basename
				if excludeNames[name] {
					return filepath.SkipDir
				}
				// Check user-specified exclude-dir by relative path prefix
				if len(excludePaths) > 0 {
					rel, _ := filepath.Rel(rootDir, path)
					for _, ep := range excludePaths {
						if rel == ep || strings.HasPrefix(rel, ep+string(filepath.Separator)) {
							return filepath.SkipDir
						}
					}
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
