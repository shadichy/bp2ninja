package generator

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type manifestXML struct {
	Package string `xml:"package,attr"`
}

type rItem struct {
	valType string
	name    string
	val     string
}

// ExtractAarClasses extracts classes.jar from an AAR. If R.txt and AndroidManifest.xml
// are present in the AAR, it generates and compiles R.java using javac, merging the
// compiled R*.class files into the destination jar alongside classes.jar.
func ExtractAarClasses(inAar, outJar string) error {
	if err := os.MkdirAll(filepath.Dir(outJar), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	zr, err := zip.OpenReader(inAar)
	if err != nil {
		return fmt.Errorf("failed to open aar %s: %w", inAar, err)
	}
	defer zr.Close()

	var classesEntry *zip.File
	var rtxtBytes []byte
	var manifestBytes []byte

	for _, f := range zr.File {
		switch f.Name {
		case "classes.jar":
			classesEntry = f
		case "R.txt":
			rc, err := f.Open()
			if err == nil {
				rtxtBytes, _ = io.ReadAll(rc)
				rc.Close()
			}
		case "AndroidManifest.xml":
			rc, err := f.Open()
			if err == nil {
				manifestBytes, _ = io.ReadAll(rc)
				rc.Close()
			}
		}
	}

	// If no R.txt exists or R.txt is empty, extract classes.jar directly.
	if len(bytes.TrimSpace(rtxtBytes)) == 0 {
		return extractRawJar(classesEntry, outJar)
	}

	// Parse package name from AndroidManifest.xml
	pkg := ""
	if len(manifestBytes) > 0 {
		var m manifestXML
		if err := xml.Unmarshal(manifestBytes, &m); err == nil {
			pkg = strings.TrimSpace(m.Package)
		}
	}
	if pkg == "" {
		// Fallback: If no manifest package, cannot place R.java properly; extract raw jar.
		return extractRawJar(classesEntry, outJar)
	}

	// Parse R.txt
	classes := make(map[string][]rItem)
	scanner := bufio.NewScanner(bytes.NewReader(rtxtBytes))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 4)
		if len(parts) < 4 {
			continue
		}
		valType := parts[0]
		resType := parts[1]
		name := parts[2]
		val := parts[3]
		classes[resType] = append(classes[resType], rItem{
			valType: valType,
			name:    name,
			val:     val,
		})
	}

	if len(classes) == 0 {
		return extractRawJar(classesEntry, outJar)
	}

	// Generate R.java
	tmpDir, err := os.MkdirTemp("", "bp2ninja-aar-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pkgSubdir := filepath.Join(strings.Split(pkg, ".")...)
	srcDir := filepath.Join(tmpDir, "src", pkgSubdir)
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		return fmt.Errorf("failed to create src dir: %w", err)
	}
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("failed to create bin dir: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("package %s;\n\npublic final class R {\n    private R() {}\n", pkg))

	var sortedTypes []string
	for rtype := range classes {
		sortedTypes = append(sortedTypes, rtype)
	}
	sort.Strings(sortedTypes)

	for _, rtype := range sortedTypes {
		sb.WriteString(fmt.Sprintf("    public static final class %s {\n        private %s() {}\n", rtype, rtype))
		for _, item := range classes[rtype] {
			val := item.val
			if item.valType == "int[]" {
				val = "new int[] " + val
			}
			sb.WriteString(fmt.Sprintf("        public static final %s %s = %s;\n", item.valType, item.name, val))
		}
		sb.WriteString("    }\n")
	}
	sb.WriteString("}\n")

	rJavaPath := filepath.Join(srcDir, "R.java")
	if err := os.WriteFile(rJavaPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write R.java: %w", err)
	}

	// Compile R.java with javac
	cmd := exec.Command("javac", "-source", "17", "-target", "17", "-d", binDir, rJavaPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Fallback without explicit -source/-target
		cmd2 := exec.Command("javac", "-d", binDir, rJavaPath)
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			return fmt.Errorf("javac failed to compile %s: %v\nOutput: %s\nFallback output: %s", rJavaPath, err, string(out), string(out2))
		}
	}

	// Create outJar merging classes.jar + compiled R*.class files
	outF, err := os.Create(outJar)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", outJar, err)
	}
	defer outF.Close()

	zw := zip.NewWriter(outF)
	writtenPaths := make(map[string]bool)

	// Copy from classes.jar if present
	if classesEntry != nil {
		rc, err := classesEntry.Open()
		if err == nil {
			jarBytes, err := io.ReadAll(rc)
			rc.Close()
			if err == nil {
				jarZr, err := zip.NewReader(bytes.NewReader(jarBytes), int64(len(jarBytes)))
				if err == nil {
					for _, jf := range jarZr.File {
						// If classes.jar already has R.class or R$*.class for this pkg, skip so generated takes precedence
						if strings.HasPrefix(jf.Name, pkgSubdir+"/R") && strings.HasSuffix(jf.Name, ".class") {
							continue
						}
						w, err := zw.Create(jf.Name)
						if err != nil {
							continue
						}
						jrc, err := jf.Open()
						if err == nil {
							_, _ = io.Copy(w, jrc)
							jrc.Close()
							writtenPaths[jf.Name] = true
						}
					}
				}
			}
		}
	}

	// Add compiled R*.class files from binDir
	err = filepath.Walk(binDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(binDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if writtenPaths[rel] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		writtenPaths[rel] = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to package classes into %s: %w", outJar, err)
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("failed to finalize %s: %w", outJar, err)
	}
	return nil
}

func extractRawJar(classesEntry *zip.File, outJar string) error {
	outF, err := os.Create(outJar)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", outJar, err)
	}
	defer outF.Close()

	if classesEntry != nil {
		rc, err := classesEntry.Open()
		if err != nil {
			return fmt.Errorf("failed to open classes.jar in aar: %w", err)
		}
		defer rc.Close()
		if _, err := io.Copy(outF, rc); err != nil {
			return fmt.Errorf("failed to write classes.jar to %s: %w", outJar, err)
		}
		return nil
	}

	// Create valid empty zip if no classes.jar exists
	zw := zip.NewWriter(outF)
	return zw.Close()
}
