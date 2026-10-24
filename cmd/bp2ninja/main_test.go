package main

import "testing"

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
