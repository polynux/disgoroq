package web

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"

	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

// templateStore holds pre-parsed page templates and fragment templates.
// To add a new page in phase 2, add its template filename to pageTemplates below.
type templateStore struct {
	pages     map[string]*template.Template
	fragments *template.Template
}

// pageTemplates maps a logical page name to the template files parsed alongside layout.html.
// Phase 2 pages can register new entries here without modifying existing templates.
var pageTemplates = map[string][]string{
	"stub":   {"templates/layout.html", "templates/stub.html"},
	"guilds": {"templates/layout.html", "templates/guilds.html"},
	"guild":  {"templates/layout.html", "templates/guild.html"},
	"config": {"templates/layout.html", "templates/config.html"},
	"data":   {"templates/layout.html", "templates/data.html"},
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{}
}

func mustLoadTemplates() *templateStore {
	funcs := templateFuncs()
	pages := make(map[string]*template.Template, len(pageTemplates))

	for name, files := range pageTemplates {
		tmpl, err := template.New("layout.html").Funcs(funcs).ParseFS(embeddedFS, files...)
		if err != nil {
			panic(fmt.Sprintf("web: failed to parse page template %q: %v", name, err))
		}
		pages[name] = tmpl
	}

	fragments, err := template.New("fragments").Funcs(funcs).ParseFS(embeddedFS, "templates/*.html")
	if err != nil {
		panic(fmt.Sprintf("web: failed to parse fragment templates: %v", err))
	}

	return &templateStore{
		pages:     pages,
		fragments: fragments,
	}
}

func (s *Server) renderPage(w http.ResponseWriter, page string, status int, data any) {
	tmpl, ok := s.templates.pages[page]
	if !ok {
		logger.Error("Web template not found", zap.String("page", page))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		logger.Error("Failed to render web page", zap.String("page", page), zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *Server) renderFragment(w http.ResponseWriter, block string, status int, data any) {
	var buf bytes.Buffer
	if err := s.templates.fragments.ExecuteTemplate(&buf, block, data); err != nil {
		logger.Error("Failed to render web fragment", zap.String("block", block), zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}
