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

// DiscoverBpFiles searches rootDir recursively for all Android.bp files.
// It skips hidden directories (names starting with '.'), "out", and "node_modules".
// Discovered files are sorted primarily by directory depth (shallower directories first)
// and secondarily by relative directory name alphabetically.
func DiscoverBpFiles(rootDir string) ([]DiscoveredBp, error) {
	var discovered []DiscoveredBp

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
		return nil, err
	}

	sort.Slice(discovered, func(i, j int) bool {
		if discovered[i].Depth != discovered[j].Depth {
			return discovered[i].Depth < discovered[j].Depth
		}
		return discovered[i].RelDir < discovered[j].RelDir
	})

	return discovered, nil
}
