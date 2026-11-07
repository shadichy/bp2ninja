package generator

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractAarClasses(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "aar-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	aarPath := filepath.Join(tmpDir, "test.aar")
	outJar := filepath.Join(tmpDir, "out.jar")

	// Create test.aar with AndroidManifest.xml and R.txt
	f, err := os.Create(aarPath)
	if err != nil {
		t.Fatalf("Create aar failed: %v", err)
	}
	zw := zip.NewWriter(f)

	manifestEntry, err := zw.Create("AndroidManifest.xml")
	if err != nil {
		t.Fatalf("Create manifest entry failed: %v", err)
	}
	_, _ = manifestEntry.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.test" >
</manifest>`))

	rtxtEntry, err := zw.Create("R.txt")
	if err != nil {
		t.Fatalf("Create R.txt entry failed: %v", err)
	}
	_, _ = rtxtEntry.Write([]byte(`int string app_name 0x7f010001
int[] styleable TestView { 0x1010001, 0x1010002 }
int styleable TestView_customAttr 0
`))

	classesEntry, err := zw.Create("classes.jar")
	if err != nil {
		t.Fatalf("Create classes.jar entry failed: %v", err)
	}
	// Write a minimal valid zip for classes.jar containing a dummy file
	cjarZw := zip.NewWriter(classesEntry)
	dummyEntry, err := cjarZw.Create("com/example/test/Dummy.class")
	if err != nil {
		t.Fatalf("Create dummy class failed: %v", err)
	}
	_, _ = dummyEntry.Write([]byte("fake class bytecode"))
	_ = cjarZw.Close()

	_ = zw.Close()
	_ = f.Close()

	if err := ExtractAarClasses(aarPath, outJar); err != nil {
		t.Fatalf("ExtractAarClasses failed: %v", err)
	}

	zr, err := zip.OpenReader(outJar)
	if err != nil {
		t.Fatalf("OpenReader on outJar failed: %v", err)
	}
	defer zr.Close()

	foundDummy := false
	foundR := false
	foundRStyleable := false

	for _, entry := range zr.File {
		if entry.Name == "com/example/test/Dummy.class" {
			foundDummy = true
		}
		if entry.Name == "com/example/test/R.class" {
			foundR = true
		}
		if entry.Name == "com/example/test/R$styleable.class" {
			foundRStyleable = true
		}
	}

	if !foundDummy {
		t.Errorf("Expected com/example/test/Dummy.class in outJar")
	}
	if !foundR {
		t.Errorf("Expected com/example/test/R.class in outJar")
	}
	if !foundRStyleable {
		t.Errorf("Expected com/example/test/R$styleable.class in outJar")
	}
}
