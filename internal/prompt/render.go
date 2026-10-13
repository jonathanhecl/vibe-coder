package prompt

import (
	"embed"
	"strings"
	"text/template"
)

// templates holds the system-prompt building blocks. They are compile-time
// assets: a missing file or a render error is a programming bug, so Render
// panics instead of degrading silently.
//
//go:embed templates/*.md
var templatesFS embed.FS

var templates = template.Must(template.ParseFS(templatesFS, "templates/*.md"))

// Render executes templates/<name>.md with data and returns the trimmed
// output. Unknown names and execution errors panic.
func Render(name string, data any) string {
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, name+".md", data); err != nil {
		panic("prompt: render " + name + ": " + err.Error())
	}
	return strings.TrimSpace(b.String())
}
