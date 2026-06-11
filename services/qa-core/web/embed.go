// Package web serves the UI: embedded HTML templates + static assets (htmx is
// vendored locally so the tool runs fully offline — no CDN). It wires the form,
// the live queue status (SSE), result rendering, and export downloads.
package web

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// parseTemplates loads every template into one set so partials and the layout
// can reference each other.
func parseTemplates() (*template.Template, error) {
	return template.New("").Funcs(funcMap).ParseFS(templatesFS, "templates/*.html")
}

var funcMap = template.FuncMap{
	"seconds": func(s float64) int { return int(s) },
}
