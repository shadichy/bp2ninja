package main

import (
	"flag"
	"strings"
	"testing"
)

func TestIsBp2SisoInvocation(t *testing.T) {
	cases := map[string]bool{
		"bp2siso":                    true,
		"bp2ninja-siso":              true,
		"./bp2siso":                  true,
		"/usr/local/bin/bp2siso":     true,
		"bp2siso.exe":                true,
		"bp2ninja":                   false,
		"./bp2ninja":                 false,
		"/usr/local/bin/bp2ninja":    false,
		"bp2ninja.exe":               false,
		"notbp2siso":                 false,
		"bp2siso-old":                false,
	}
	for argv0, want := range cases {
		if got := isBp2SisoInvocation(argv0); got != want {
			t.Errorf("isBp2SisoInvocation(%q) = %v, want %v", argv0, got, want)
		}
	}
}

func TestIsBp2CargoInvocation(t *testing.T) {
	cases := map[string]bool{
		"bp2cargo":                    true,
		"bp2ninja-cargo":              true,
		"./bp2cargo":                  true,
		"/usr/local/bin/bp2cargo":     true,
		"bp2cargo.exe":                true,
		"bp2ninja":                    false,
		"./bp2ninja":                  false,
		"/usr/local/bin/bp2ninja":     false,
		"bp2ninja.exe":                false,
		"notbp2cargo":                 false,
		"bp2cargo-old":                false,
	}
	for argv0, want := range cases {
		if got := isBp2CargoInvocation(argv0); got != want {
			t.Errorf("isBp2CargoInvocation(%q) = %v, want %v", argv0, got, want)
		}
	}
}

func TestPrintUsageGNUFormat(t *testing.T) {
	var buf strings.Builder
	printUsage(&buf, "bp2ninja")
	out := buf.String()

	requiredHeaders := []string{
		"Usage: bp2ninja [OPTION]... [FILE|DIR]",
		"Core options:",
		"Target architecture and NDK options:",
		"Compiler and linker overrides:",
		"Java and Kotlin toolchain options:",
		"Rust and Cargo options (bp2cargo):",
		"Platform codegen tools:",
		"Siso execution options (bp2siso):",
		"Modular plugins and Go workspace:",
		"Informational options:",
		"Subcommands:",
		"Environment variables:",
		"-cflags, -ccflags FLAGS",
		"-cppflags, -cxxflags FLAGS",
		"-bootclasspath PATHS",
		"-prebuilt-jars PATHS",
		"-extra-jars PATHS",
		"-android-jar PATH",
		"Report bugs to:",
	}

	for _, header := range requiredHeaders {
		if !strings.Contains(out, header) {
			t.Errorf("printUsage missing expected header or entry %q:\n%s", header, out)
		}
	}

	// Test bp2cargo invocation variant
	var cargoBuf strings.Builder
	printUsage(&cargoBuf, "bp2cargo")
	cargoOut := cargoBuf.String()
	if !strings.Contains(cargoOut, "Usage: bp2cargo [OPTION]... [FILE|DIR]") {
		t.Errorf("printUsage for bp2cargo missing Usage line:\n%s", cargoOut)
	}
	if strings.Contains(cargoOut, "or:  bp2cargo") {
		t.Errorf("printUsage for bp2cargo should not list bp2cargo in 'or:' lines:\n%s", cargoOut)
	}
}

func TestCcflagsAlias(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var extraCflags string
	var extraCcflags string
	fs.StringVar(&extraCflags, "cflags", "", "")
	fs.StringVar(&extraCcflags, "ccflags", "", "")

	if err := fs.Parse([]string{"-ccflags", "-Wall -Werror"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if extraCcflags != "-Wall -Werror" {
		t.Errorf("expected extraCcflags to be '-Wall -Werror', got %q", extraCcflags)
	}
}

func TestPathListFlag(t *testing.T) {
	var list pathListFlag
	if err := list.Set("/a/b.jar:/c/d.jar"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if len(list) != 2 || list[0] != "/a/b.jar" || list[1] != "/c/d.jar" {
		t.Errorf("Unexpected path list: %v", list)
	}
	if list.String() != "/a/b.jar:/c/d.jar" {
		t.Errorf("Unexpected string output: %s", list.String())
	}

	// Comma separated fallback
	var commaList pathListFlag
	if err := commaList.Set("/x.jar, /y.jar"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if len(commaList) != 2 || commaList[0] != "/x.jar" || commaList[1] != "/y.jar" {
		t.Errorf("Unexpected comma path list: %v", commaList)
	}
}
