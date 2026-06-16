package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	pastebox "pastebox/internal"
)

func (a *app) deleteHandler(w http.ResponseWriter, r *http.Request, id string, token string) {
	if r.Method == http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := a.store.Delete(id, token); err != nil {
		if errors.Is(err, pastebox.ErrInvalidDeleteToken) {
			logEvent("paste.delete_denied", map[string]any{
				"id":     id,
				"remote": r.RemoteAddr,
			})
			http.Error(w, "delete token required or invalid", http.StatusUnauthorized)
			return
		}

		http.NotFound(w, r)
		return
	}

	logEvent("paste.deleted", map[string]any{
		"id":     id,
		"remote": r.RemoteAddr,
	})

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "deleted")
}

func (a *app) viewHandler(w http.ResponseWriter, r *http.Request, id string) {
	password := r.URL.Query().Get("password")
	if password == "" {
		password = r.Header.Get("paste-password")
	}

	if r.Method == http.MethodHead {
		entry, err := a.store.Open(id, password)
		if err != nil {
			if errors.Is(err, pastebox.ErrInvalidPassword) {
				a.passwordRequiredHandler(w, r, id)
				return
			}
			a.notFoundHandler(w, r)
			return
		}
		defer entry.File.Close()

		a.writeViewHeaders(w, entry)
		return
	}

	err := a.store.View(id, password, func(entry *pastebox.Entry) error {
		raw := responseFormat(r) == "raw"
		browser := isBrowserRequest(r)

		if !raw && browser && isTextEntry(entry) {
			content, err := io.ReadAll(entry.File)
			if err != nil {
				return err
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)

			return a.paste.Execute(w, map[string]any{
				"ID":       entry.Meta.ID,
				"Filename": entry.Meta.Filename,
				"Content":  string(content),
				"Language": syntaxLanguage(entry.Meta.Filename, entry.Meta.ContentType),
				"Password": password,
			})
		}

		a.writeViewHeaders(w, entry)
		_, err := io.Copy(w, entry.File)
		return err
	})
	if err != nil {
		if errors.Is(err, pastebox.ErrInvalidPassword) {
			a.passwordRequiredHandler(w, r, id)
			return
		}
		a.notFoundHandler(w, r)
		return
	}
}

func (a *app) writeViewHeaders(w http.ResponseWriter, entry *pastebox.Entry) {
	contentType := entry.Meta.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if isTextEntry(entry) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, entry.Meta.ID))
}

func (a *app) passwordRequiredHandler(w http.ResponseWriter, r *http.Request, id string) {
	if !isBrowserRequest(r) {
		http.Error(w, "password required or invalid. use ?password=... or paste-password header", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)

	_ = a.passwordPage.Execute(w, map[string]any{
		"ID":     id,
		"Action": "/" + id,
	})
}
