package main

import (
	"errors"
	"net/http"
	"strings"

	pastebox "pastebox/internal"
)

func (a *app) cloneHandler(w http.ResponseWriter, r *http.Request, id string) {
	disabled, err := a.store.UploadsDisabled()
	if err != nil {
		logEvent("uploads.status_read_failed", map[string]any{
			"error": err,
		})
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
	newPassword := r.Header.Get("password")
	policy, err := pastebox.ParseDataPolicy(r.Header.Get("data-policy"))
	if err != nil {
		a.respondRequestError(w, r, http.StatusBadRequest, "invalid data-policy. use temporary, permanent, once, or a duration up to 30d like 30m, 12h, 7d")
		return
	}
	customCode := strings.TrimSpace(r.Header.Get("code"))

	meta, generatedPassword, _, manageToken, err := a.store.CloneWithPassword(id, password, usePassword, newPassword, policy, customCode)
	if err != nil {
		if errors.Is(err, pastebox.ErrInvalidPassword) {
			if isBrowserRequest(r) {
				a.renderPasswordPage(w, r, id, "/"+id+"?clone=1", http.MethodPost, a.localizedText("paste_clone", "Clone"))
				return
			}

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

		if errors.Is(err, pastebox.ErrInvalidNewPassword) {
			a.respondRequestError(w, r, http.StatusBadRequest, "invalid password header. use 8-128 characters without control characters and do not combine it with usepassword")
			return
		}

		http.NotFound(w, r)
		return
	}

	logEvent("paste.cloned", map[string]any{
		"content_type": meta.ContentType,
		"expires":      formatExpiresForLog(meta),
		"id":           meta.ID,
		"policy":       meta.DataPolicy,
		"protected":    generatedPassword != "" || newPassword != "",
		"remote":       r.RemoteAddr,
		"size":         meta.Size,
		"source":       id,
	})
	a.notifyDiscordPasteCreated(r, meta, newPassword != "", id)

	a.writeCloneResponse(w, r, meta, generatedPassword, manageToken)
}
