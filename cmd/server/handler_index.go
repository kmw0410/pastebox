package main

import "net/http"

func (a *app) indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	data := map[string]string{
		"BaseURL": requestBaseURL(r),
	}

	if err := a.index.Execute(w, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (a *app) notFoundHandler(w http.ResponseWriter, r *http.Request) {
	if !isBrowserRequest(r) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)

	_ = a.notFoundPage.Execute(w, map[string]any{
		"Path": r.URL.Path,
	})
}
