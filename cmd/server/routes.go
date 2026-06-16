package main

import (
	"net/http"
	"strings"
)

func (a *app) handle(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/admin") {
		a.adminHandler(w, r)
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
