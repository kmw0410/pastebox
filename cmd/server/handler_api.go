package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	pastebox "pastebox/internal"
)

const (
	manageTokenHeader = "paste-manage-token"
	deleteTokenHeader = "paste-delete-token"
	maxAPIRequestBody = 64 << 10
)

type pasteAPIResponse struct {
	ID                string `json:"id"`
	Filename          string `json:"filename,omitempty"`
	Label             string `json:"label,omitempty"`
	PublicURL         string `json:"url"`
	CreatedAt         string `json:"created_at"`
	ExpiresAt         string `json:"expires,omitempty"`
	DataPolicy        string `json:"data_policy"`
	Size              int64  `json:"size"`
	ContentType       string `json:"content_type"`
	PasswordProtected bool   `json:"password_protected"`
	Password          string `json:"password,omitempty"`
}

type pasteAPIPatchRequest struct {
	Action      string `json:"action"`
	Label       string `json:"label,omitempty"`
	DataPolicy  string `json:"data_policy,omitempty"`
	NewPassword string `json:"new_password,omitempty"`
	Password    string `json:"password,omitempty"`
}

func (a *app) pasteAPIHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/pastes/")
	if id == "" || strings.Contains(id, "/") {
		a.writeAPIError(w, http.StatusNotFound, "paste not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.getManagedPasteAPI(w, r, id)
	case http.MethodPatch:
		a.patchManagedPasteAPI(w, r, id)
	case http.MethodDelete:
		a.deletePasteAPI(w, r, id)
	default:
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		a.writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *app) getManagedPasteAPI(w http.ResponseWriter, r *http.Request, id string) {
	meta, err := a.store.ManageMetadata(id, r.Header.Get(manageTokenHeader))
	if err != nil {
		a.writePasteAPIStoreError(w, err)
		return
	}
	a.writePasteAPIResponse(w, r, meta, "")
}

func (a *app) patchManagedPasteAPI(w http.ResponseWriter, r *http.Request, id string) {
	var request pasteAPIPatchRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		a.writeAPIError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		a.writeAPIError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}

	token := r.Header.Get(manageTokenHeader)
	var (
		meta              pastebox.Metadata
		generatedPassword string
		err               error
	)
	switch strings.ToLower(strings.TrimSpace(request.Action)) {
	case "set_label":
		meta, err = a.store.SetLabel(id, token, request.Label)
	case "set_policy":
		meta, err = a.store.SetDataPolicy(id, token, request.DataPolicy)
	case "enable_password":
		meta, generatedPassword, err = a.store.SetPasswordProtectionWithPassword(id, token, request.NewPassword)
	case "disable_password":
		meta, err = a.store.ClearPasswordProtection(id, token, request.Password)
	default:
		a.writeAPIError(w, http.StatusBadRequest, "invalid action")
		return
	}
	if err != nil {
		a.writePasteAPIStoreError(w, err)
		return
	}
	a.writePasteAPIResponse(w, r, meta, generatedPassword)
}

func (a *app) deletePasteAPI(w http.ResponseWriter, r *http.Request, id string) {
	manageToken := strings.TrimSpace(r.Header.Get(manageTokenHeader))
	deleteToken := strings.TrimSpace(r.Header.Get(deleteTokenHeader))
	if (manageToken == "") == (deleteToken == "") {
		a.writeAPIError(w, http.StatusBadRequest, "provide exactly one management or delete token")
		return
	}

	var err error
	if manageToken != "" {
		err = a.store.DeleteManaged(id, manageToken)
	} else {
		err = a.store.Delete(id, deleteToken)
	}
	if err != nil {
		a.writePasteAPIStoreError(w, err)
		return
	}

	a.notifyDiscordPasteDeleted(id, "API")
	a.writeAPIJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

func (a *app) writePasteAPIResponse(w http.ResponseWriter, r *http.Request, meta pastebox.Metadata, generatedPassword string) {
	response := pasteAPIResponse{
		ID:                meta.ID,
		Filename:          meta.Filename,
		Label:             meta.Label,
		PublicURL:         strings.TrimRight(requestBaseURL(r), "/") + "/" + meta.ID,
		CreatedAt:         meta.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		ExpiresAt:         formatExpiresForResponse(meta),
		DataPolicy:        meta.DataPolicy,
		Size:              meta.Size,
		ContentType:       meta.ContentType,
		PasswordProtected: meta.PasswordHash != "",
		Password:          generatedPassword,
	}
	a.writeAPIJSON(w, http.StatusOK, response)
}

func (a *app) writePasteAPIStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pastebox.ErrInvalidPolicy):
		a.writeAPIError(w, http.StatusBadRequest, "invalid data policy")
	case errors.Is(err, pastebox.ErrInvalidLabel):
		a.writeAPIError(w, http.StatusBadRequest, "invalid label")
	case errors.Is(err, pastebox.ErrInvalidNewPassword):
		a.writeAPIError(w, http.StatusBadRequest, "invalid new password")
	case errors.Is(err, pastebox.ErrAlreadyProtected):
		a.writeAPIError(w, http.StatusConflict, "paste is already password protected")
	case errors.Is(err, pastebox.ErrInvalidPassword):
		a.writeAPIError(w, http.StatusUnauthorized, "password required or invalid")
	case errors.Is(err, pastebox.ErrNotFound), errors.Is(err, pastebox.ErrInvalidManageToken), errors.Is(err, pastebox.ErrInvalidDeleteToken):
		a.writeAPIError(w, http.StatusNotFound, "paste not found or token invalid")
	default:
		a.writeAPIError(w, http.StatusInternalServerError, "request failed")
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("extra JSON value")
	}
	return nil
}

func (a *app) writeAPIError(w http.ResponseWriter, status int, message string) {
	a.writeAPIJSON(w, status, map[string]string{"error": message})
}

func (a *app) writeAPIJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(status)
	_ = writePrettyJSON(w, value)
}
