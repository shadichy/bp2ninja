package generator

import "testing"

// TestShellJoinValueFlags guards the value-taking flag pairs (e.g. -include,
// -I) against being dropped as "dangling prefixes". A flag followed by a value
// must emit both tokens; only a genuinely trailing flag is dropped.
func TestShellJoinValueFlags(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{
			name: "include keeps its target",
			in:   []string{"-fPIC", "-include", "util/host_prelude.h", "-c", "x.cpp"},
			want: "-fPIC -include util/host_prelude.h -c x.cpp",
		},
		{
			name: "isystem keeps its target",
			in:   []string{"-isystem", "/usr/include", "-c", "x.cpp"},
			want: "-isystem /usr/include -c x.cpp",
		},
		{
			name: "include target needs quoting",
			in:   []string{"-include", "dir with space/hdr.h"},
			want: "-include 'dir with space/hdr.h'",
		},
		{
			name: "dangling -I is dropped",
			in:   []string{"-fPIC", "-I"},
			want: "-fPIC",
		},
		{
			name: "empty value treated as dangling",
			in:   []string{"-I", "", "-c", "x.cpp"},
			want: "-c x.cpp",
		},
		{
			name: "joined -include form still works",
			in:   []string{"-include util/prelude.h"},
			want: "-include util/prelude.h",
		},
		{
			name: "multiple includes preserved",
			in:   []string{"-include", "a.h", "-include", "b.h"},
			want: "-include a.h -include b.h",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shellJoin(tc.in)
			if got != tc.want {
				t.Fatalf("shellJoin(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
