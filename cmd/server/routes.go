package main

import (
	"net/http"
	"strings"
)

// Apply privacy headers before ServeMux can emit redirects or errors. Static
// asset handlers override Cache-Control with their existing public policies.
func (a *app) httpHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/css/", staticFileHandler("/css/", "templates/css", "public, max-age=3600"))
	mux.Handle("/js/", staticFileHandler("/js/", "templates/js", "public, max-age=31536000, immutable"))
	mux.HandleFunc("/", a.handle)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}

func (a *app) handle(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/v1/pastes/") {
		a.pasteAPIHandler(w, r)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/admin") {
		a.adminHandler(w, r)
		return
	}

	if r.URL.Path == "/healthz" {
		a.healthHandler(w, r)
		return
	}

	if r.URL.Path == "/" {
		switch r.Method {
		case http.MethodGet:
			a.indexHandler(w, r)
		case http.MethodPost, http.MethodPut:
			a.uploadHandler(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/")
	if strings.Contains(id, "/") || id == "" {
		a.notFoundHandler(w, r)
		return
	}

	if r.Method == http.MethodPost {
		if r.URL.Query().Get("manage") != "" {
			a.manageHandler(w, r, id)
			return
		}
		a.cloneHandler(w, r, id)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if token := r.URL.Query().Get("delete"); token != "" {
		a.deleteHandler(w, r, id, token)
		return
	}

	if token := r.URL.Query().Get("manage"); token != "" {
		a.manageHandler(w, r, id)
		return
	}

	a.viewHandler(w, r, id)
}
