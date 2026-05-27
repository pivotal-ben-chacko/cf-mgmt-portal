package http

import (
	"embed"
	"html/template"
	"log"
	"net/http"
)

//go:embed templates/*.html
var templatesFS embed.FS

var tpl = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

func renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

// renderError renders error.html at the given status. Falls back to plain text
// if the template itself fails so callers always get something.
func renderError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	err := tpl.ExecuteTemplate(w, "error.html", map[string]any{
		"Status":     status,
		"StatusText": http.StatusText(status),
		"Message":    message,
	})
	if err != nil {
		log.Printf("render error.html: %v", err)
		_, _ = w.Write([]byte(message))
	}
}
