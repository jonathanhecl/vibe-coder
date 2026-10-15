package lsp

import (
	"testing"
)

func TestLanguageReportsTheOwningFamily(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{name: "go", path: "main.go", want: "go"},
		{name: "extension case", path: "Main.GO", want: "go"},
		{name: "nested path", path: "internal/lsp/manager.go", want: "go"},
		{name: "unicode path", path: "ñandú/código.go", want: "go"},
		{name: "surrounding spaces", path: "  spaced.go  ", want: "go"},
		{name: "tsx belongs to typescript", path: "src/app.tsx", want: "typescript"},
		{name: "javascript belongs to typescript", path: "src/app.js", want: "typescript"},
		{name: "module javascript", path: "src/app.mjs", want: "typescript"},
		{name: "rust", path: "lib.rs", want: "rust"},
		{name: "python stub", path: "mod.pyi", want: "python"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Language(tc.path)
			if !ok {
				t.Fatalf("Language(%q) = not supported, want %s", tc.path, tc.want)
			}
			if got != tc.want {
				t.Errorf("Language(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestLanguageRejectsFilesNoServerOwns(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "markdown", path: "README.md"},
		{name: "extensionless", path: "Makefile"},
		{name: "no extension at all", path: "noextension"},
		{name: "empty", path: ""},
		{name: "yaml", path: "config.yaml"},
		{name: "dotfile", path: ".gitignore"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if language, ok := Language(tc.path); ok {
				t.Errorf("Language(%q) = %q, want no language", tc.path, language)
			}
		})
	}
}

func TestDocumentLanguageIDDistinguishesFlavors(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{path: "a.ts", want: "typescript"},
		{path: "a.tsx", want: "typescriptreact"},
		{path: "a.js", want: "javascript"},
		{path: "a.jsx", want: "javascriptreact"},
		{path: "a.mjs", want: "javascript"},
		{path: "a.pyi", want: "python"},
		{path: "a.rs", want: "rust"},
		{path: "a.go", want: "go"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := documentLanguageID(tc.path); got != tc.want {
				t.Errorf("documentLanguageID(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// A server is sent the same language id whatever the file extension is spelled
// like, and no extension can be sent an empty one: a server that does not
// recognise the id publishes nothing, which would look like a clean file.
func TestEveryExtensionOfEveryServerHasALanguageID(t *testing.T) {
	for _, spec := range defaultServers {
		for _, ext := range spec.extensions {
			if id := documentLanguageID("file" + ext); id == "" {
				t.Errorf("%s: extension %s has no language id", spec.language, ext)
			}
		}
	}
}

func TestEveryServerIsFullyDescribed(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range defaultServers {
		name := spec.language
		if seen[name] {
			t.Errorf("family %q is declared twice", name)
		}
		seen[name] = true
		if len(spec.markers) == 0 {
			t.Errorf("%s: no workspace marker, so it could never be detected", name)
		}
		if len(spec.commands) == 0 {
			t.Errorf("%s: no command to run", name)
		}
		if len(spec.extensions) == 0 {
			t.Errorf("%s: no extension to claim", name)
		}
	}
}

// Two families claiming one extension would make Language's answer depend on
// map ordering, which would make detection nondeterministic.
func TestNoExtensionIsClaimedTwice(t *testing.T) {
	owners := map[string]string{}
	for _, spec := range defaultServers {
		for _, ext := range spec.extensions {
			if owner, taken := owners[ext]; taken {
				t.Errorf("extension %s is claimed by %s and %s", ext, owner, spec.language)
				continue
			}
			owners[ext] = spec.language
		}
	}
	for ext, owner := range owners {
		if language, ok := Language("file" + ext); !ok || language != owner {
			t.Errorf("Language(file%s) = %q, %v, want %s", ext, language, ok, owner)
		}
	}
}
