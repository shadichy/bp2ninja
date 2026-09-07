package ndk

import (
	"testing"
)

func TestDiscoverAll(t *testing.T) {
	ndks := DiscoverAll()
	if len(ndks) == 0 {
		t.Log("No NDK found on host system (acceptable in minimal CI)")
		return
	}

	t.Logf("Discovered %d NDK(s):", len(ndks))
	for i, n := range ndks {
		t.Logf(" [%d] Version: %s (major: %d, beta: %v) from %s -> %s", i, n.Version, n.MajorVer, n.IsBeta, n.Source, n.Path)
	}

	// Test toolchain resolution for x86_64
	primary := ndks[0]
	tc, err := primary.GetToolchain("x86_64", 34)
	if err != nil {
		t.Fatalf("Failed to resolve toolchain: %v", err)
	}
	t.Logf("Toolchain x86_64: CC=%s, CXX=%s, AR=%s, Sysroot=%s", tc.CC, tc.CXX, tc.AR, tc.Sysroot)

	// Test toolchain resolution for arm64
	tcArm, err := primary.GetToolchain("arm64", 34)
	if err != nil {
		t.Fatalf("Failed to resolve arm64 toolchain: %v", err)
	}
	t.Logf("Toolchain arm64: CC=%s, CXX=%s, AR=%s", tcArm.CC, tcArm.CXX, tcArm.AR)
}

func TestResolveNDK(t *testing.T) {
	ndks := DiscoverAll()
	if len(ndks) == 0 {
		return
	}

	// Resolve auto
	ndkAuto, err := ResolveNDK("auto", "")
	if err != nil {
		t.Fatalf("ResolveNDK auto failed: %v", err)
	}
	t.Logf("Resolved auto NDK: %s (%s)", ndkAuto.Version, ndkAuto.Path)

	// Resolve beta if present
	if ndkBeta, err := ResolveNDK("", "beta"); err == nil {
		t.Logf("Resolved beta NDK: %s (%s)", ndkBeta.Version, ndkBeta.Path)
	}
}
