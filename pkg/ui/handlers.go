package ui

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

func handleAnalyses(base *template.Template) (http.HandlerFunc, error) {
	tmpl, err := clonePage(base, "templates/analyses.html")
	if err != nil {
		return nil, err
	}
	return func(w http.ResponseWriter, r *http.Request) {
		render(w, tmpl, map[string]any{
			"Page":          "analyses",
			"AnalysisName":  "",
			"OperationName": "",
		})
	}, nil
}

func handleCreate(base *template.Template) (http.HandlerFunc, error) {
	tmpl, err := clonePage(base, "templates/create.html")
	if err != nil {
		return nil, err
	}
	return func(w http.ResponseWriter, r *http.Request) {
		render(w, tmpl, map[string]any{
			"Page":          "create",
			"AnalysisName":  "",
			"OperationName": "",
		})
	}, nil
}

func handleAnalysis(base *template.Template) (http.HandlerFunc, error) {
	tmpl, err := clonePage(base, "templates/analysis.html")
	if err != nil {
		return nil, err
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// Path: /ui/a/{analyses/uuid}
		// Strip "/ui/a/" to get the full AIP resource name.
		analysisName := strings.TrimPrefix(r.URL.Path, "/ui/a/")
		if analysisName == "" || !strings.HasPrefix(analysisName, "analyses/") {
			http.NotFound(w, r)
			return
		}

		// Derive operation name: "analyses/{uuid}" → "operations/{uuid}"
		uuid := strings.TrimPrefix(analysisName, "analyses/")
		opName := "operations/" + uuid

		render(w, tmpl, map[string]any{
			"Page":          "analysis",
			"AnalysisName":  analysisName,
			"OperationName": opName,
		})
	}, nil
}

func clonePage(base *template.Template, pageFile string) (*template.Template, error) {
	t, err := base.Clone()
	if err != nil {
		return nil, fmt.Errorf("clone base: %w", err)
	}
	t, err = t.ParseFS(assets, pageFile)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", pageFile, err)
	}
	return t, nil
}

func render(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, "render error: "+err.Error(), http.StatusInternalServerError)
	}
}
