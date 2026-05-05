package ui

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static templates
var assets embed.FS

// Register mounts all UI routes onto mux under /ui/.
// Returns an error if templates fail to parse.
//
// URL scheme:
//
//	GET /ui/                     analyses list
//	GET /ui/create               new analysis form
//	GET /ui/a/{analyses/uuid}    analysis detail
func Register(mux *http.ServeMux) error {
	base, err := buildBase()
	if err != nil {
		return fmt.Errorf("ui: build base template: %w", err)
	}

	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		return fmt.Errorf("ui: sub static fs: %w", err)
	}
	mux.Handle("/ui/static/", http.StripPrefix("/ui/static/",
		http.FileServer(http.FS(staticFS)),
	))

	analysesFn, err := handleAnalyses(base)
	if err != nil {
		return fmt.Errorf("ui: handleAnalyses: %w", err)
	}

	createFn, err := handleCreate(base)
	if err != nil {
		return fmt.Errorf("ui: handleCreate: %w", err)
	}

	analysisFn, err := handleAnalysis(base)
	if err != nil {
		return fmt.Errorf("ui: handleAnalysis: %w", err)
	}

	mux.HandleFunc("/ui/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/ui/")

		switch {
		case path == "" || path == "/":
			analysesFn(w, r)

		case path == "create":
			createFn(w, r)

		case strings.HasPrefix(path, "a/"):
			analysisFn(w, r)

		default:
			http.NotFound(w, r)
		}
	})

	return nil
}

func buildBase() (*template.Template, error) {
	tmpl, err := template.New("layout.html").
		Funcs(template.FuncMap{"lower": strings.ToLower}).
		ParseFS(assets,
			"templates/layout.html",
			"templates/partials/state_badge.html",
		)
	if err != nil {
		return nil, err
	}
	return tmpl, nil
}
