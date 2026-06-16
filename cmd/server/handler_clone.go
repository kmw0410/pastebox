package main

import (
	"errors"
	"log"
	"net/http"
	"strings"

	pastebox "pastebox/internal"
)

func (a *app) cloneHandler(w http.ResponseWriter, r *http.Request, id string) {
	disabled, err := a.store.UploadsDisabled()
	if err != nil {
		log.Printf("failed to read upload status: %v", err)
		http.Error(w, "upload status unavailable", http.StatusInternalServerError)
		return
	}

	if disabled {
		a.respondRequestError(w, r, http.StatusServiceUnavailable, "new uploads are currently disabled")
		return
	}

	password := r.FormValue("password")
	if password == "" {
		password = r.URL.Query().Get("password")
	}
	if password == "" {
		password = r.Header.Get("paste-password")
	}

	usePassword := strings.EqualFold(strings.TrimSpace(r.Header.Get("usepassword")), "true")
	policy := strings.ToLower(strings.TrimSpace(r.Header.Get("data-policy")))
	permanent := policy == "permanent"
	once := policy == "once"
	customCode := strings.TrimSpace(r.Header.Get("code"))

	meta, newPassword, deleteToken, manageToken, err := a.store.Clone(id, password, usePassword, permanent, once, customCode)
	if err != nil {
		if errors.Is(err, pastebox.ErrInvalidPassword) {
			a.respondRequestError(w, r, http.StatusUnauthorized, "password required or invalid. use ?password=... or paste-password header")
			return
		}

		if errors.Is(err, pastebox.ErrInvalidCode) {
			a.respondRequestError(w, r, http.StatusBadRequest, "invalid code. use 1-10 characters: letters, numbers, underscore, or hyphen")
			return
		}

		if errors.Is(err, pastebox.ErrCodeExists) {
			a.respondRequestError(w, r, http.StatusConflict, "code already exists")
			return
		}

		http.NotFound(w, r)
		return
	}

	log.Printf(
		"cloned: source=%s id=%s remote=%s size=%d content_type=%q policy=%s expires=%s protected=%t",
		id,
		meta.ID,
		r.RemoteAddr,
		meta.Size,
		meta.ContentType,
		meta.DataPolicy,
		formatExpiresForLog(meta),
		newPassword != "",
	)

	a.writeCloneResponse(w, r, meta, newPassword, deleteToken, manageToken)
}
