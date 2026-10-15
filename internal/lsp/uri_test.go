package lsp

import "testing"

func TestFileURIEscapesThePath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{name: "plain", path: "/tmp/main.go", want: "file:///tmp/main.go"},
		{name: "space", path: "/tmp/my project/main.go", want: "file:///tmp/my%20project/main.go"},
		{name: "unicode", path: "/tmp/ñ/café.go", want: "file:///tmp/%C3%B1/caf%C3%A9.go"},
		{name: "quote", path: `/tmp/it's/main.go`, want: "file:///tmp/it%27s/main.go"},
		{name: "relative gets a root slash", path: "main.go", want: "file:///main.go"},
		// Already-slashed input, so the Windows case is checked on every
		// platform: filepath.ToSlash turns the separator into "/" there, and the
		// drive path then needs the leading slash a URI demands.
		{name: "windows drive", path: "C:/proj/main.go", want: "file:///C:/proj/main.go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fileURI(tc.path); got != tc.want {
				t.Errorf("fileURI(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
