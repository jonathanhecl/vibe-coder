package lsp

import (
	"path/filepath"
	"strings"
)

// serverSpec describes one language server family: the workspace markers that
// select it, the command lines that can run it, and the files whose diagnostics
// it owns.
type serverSpec struct {
	language   string
	markers    []string
	commands   [][]string
	extensions []string
}

// defaultServers lists the language servers this client knows how to run.
//
// The marker order mirrors the auto-test detection order in
// internal/git/autotest.go, so a polyglot workspace resolves the same way for
// both features: the first marker found wins. Commands are candidates in
// preference order; the first one whose binary is on PATH is the one used.
var defaultServers = []serverSpec{
	{
		language:   "python",
		markers:    []string{"pyproject.toml", "pytest.ini", "setup.py", "setup.cfg", "requirements.txt"},
		commands:   [][]string{{"ruff", "server"}, {"pylsp"}},
		extensions: []string{".py", ".pyi"},
	},
	{
		language:   "typescript",
		markers:    []string{"package.json", "tsconfig.json", "jsconfig.json"},
		commands:   [][]string{{"typescript-language-server", "--stdio"}},
		extensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
	},
	{
		language:   "rust",
		markers:    []string{"Cargo.toml"},
		commands:   [][]string{{"rust-analyzer"}},
		extensions: []string{".rs"},
	},
	{
		language:   "go",
		markers:    []string{"go.mod"},
		commands:   [][]string{{"gopls"}},
		extensions: []string{".go"},
	},
}

// documentLanguageIDs maps a file extension to the languageId sent on
// textDocument/didOpen. It is a client-side identifier that servers key their
// behavior on, which is why it is not simply the family name:
// typescript-language-server distinguishes .tsx from .ts and would parse a .tsx
// file as plain TypeScript without it.
var documentLanguageIDs = map[string]string{
	".go":  "go",
	".py":  "python",
	".pyi": "python",
	".rs":  "rust",
	".ts":  "typescript",
	".tsx": "typescriptreact",
	".js":  "javascript",
	".jsx": "javascriptreact",
	".mjs": "javascript",
	".cjs": "javascript",
}

// extensionOwner maps a file extension to the language family that owns it.
// Built once from defaultServers so the table stays the single source of truth.
var extensionOwner = buildExtensionOwner()

func buildExtensionOwner() map[string]string {
	owners := make(map[string]string)
	for _, spec := range defaultServers {
		for _, ext := range spec.extensions {
			if _, taken := owners[ext]; !taken {
				owners[ext] = spec.language
			}
		}
	}
	return owners
}

// specFor returns the family definition for a language key.
func specFor(language string) (serverSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(language))
	for _, spec := range defaultServers {
		if spec.language == key {
			return spec, true
		}
	}
	return serverSpec{}, false
}

// Language reports the language server family that owns a file, from its
// extension alone and without touching the workspace. Callers that need to know
// whether a server is actually available use Manager.Supports.
func Language(filePath string) (string, bool) {
	owner, ok := extensionOwner[documentExtension(filePath)]
	return owner, ok
}

// documentExtension normalizes the extension of a path the model supplied,
// which may carry stray whitespace or an unexpected case.
func documentExtension(filePath string) string {
	return strings.ToLower(filepath.Ext(strings.TrimSpace(filePath)))
}

// documentLanguageID is the languageId reported for an open document. An
// extension without a more specific identifier falls back to its family name so
// a server still receives something it can route on.
func documentLanguageID(filePath string) string {
	ext := documentExtension(filePath)
	if id, ok := documentLanguageIDs[ext]; ok {
		return id
	}
	if owner, ok := extensionOwner[ext]; ok {
		return owner
	}
	return ""
}
