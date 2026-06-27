package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	pastebox "pastebox/internal"
)

type managePageData struct {
	ID                string
	Filename          string
	PublicURL         string
	ManageURL         string
	CreatedAt         time.Time
	ExpiresAt         time.Time
	DataPolicy        string
	PasswordProtected bool
	Notice            string
	Error             string
	GeneratedPassword string
	Deleted           bool
	PolicyKind        string
	CustomPolicyValue string
}

func (a *app) manageHandler(w http.ResponseWriter, r *http.Request, id string) {
	token := strings.TrimSpace(r.URL.Query().Get("manage"))
	if token == "" {
		a.notFoundHandler(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		meta, err := a.store.ManageMetadata(id, token)
		if err != nil {
			a.notFoundHandler(w, r)
			return
		}

		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}

		a.renderManagePage(w, r, meta, token, "", "", "", false)
		return
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			a.renderManageError(w, r, id, token, a.i18n.T("manage_error_invalid_form"))
			return
		}

		action := strings.ToLower(strings.TrimSpace(r.FormValue("manage_action")))
		switch action {
		case "enable_password":
			meta, password, err := a.store.SetPasswordProtection(id, token)
			if err != nil {
				a.renderManageError(w, r, id, token, a.manageErrorMessage(err))
				return
			}
			a.renderManagePage(w, r, meta, token, a.i18n.T("manage_password_enabled"), "", password, false)
		case "disable_password":
			password := r.FormValue("password")
			meta, err := a.store.ClearPasswordProtection(id, token, password)
			if err != nil {
				a.renderManageError(w, r, id, token, a.manageErrorMessage(err))
				return
			}
			a.renderManagePage(w, r, meta, token, a.i18n.T("manage_password_disabled"), "", "", false)
		case "set_policy":
			policy, err := parseManagePolicyForm(r)
			if err != nil {
				a.renderManageError(w, r, id, token, a.manageErrorMessage(err))
				return
			}
			meta, err := a.store.SetDataPolicy(id, token, policy)
			if err != nil {
				a.renderManageError(w, r, id, token, a.manageErrorMessage(err))
				return
			}
			a.renderManagePage(w, r, meta, token, a.i18n.T("manage_policy_updated"), "", "", false)
		case "delete":
			if err := a.store.DeleteManaged(id, token); err != nil {
				a.renderManageError(w, r, id, token, a.manageErrorMessage(err))
				return
			}
			a.renderManageDeleted(w, r, id, token)
		default:
			a.renderManageError(w, r, id, token, a.i18n.T("manage_error_invalid_action"))
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *app) renderManageDeleted(w http.ResponseWriter, r *http.Request, id string, token string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	baseURL := requestBaseURL(r)
	publicURL := strings.TrimRight(baseURL, "/") + "/" + id

	_ = a.managePage.Execute(w, managePageData{
		ID:        id,
		PublicURL: publicURL,
		ManageURL: publicURL + "?manage=" + token,
		Notice:    a.i18n.T("manage_deleted"),
		Deleted:   true,
	})
}

func (a *app) renderManageError(w http.ResponseWriter, r *http.Request, id string, token string, message string) {
	meta, err := a.store.ManageMetadata(id, token)
	if err != nil {
		a.notFoundHandler(w, r)
		return
	}
	a.renderManagePage(w, r, meta, token, "", message, "", false)
}

func (a *app) renderManagePage(w http.ResponseWriter, r *http.Request, meta pastebox.Metadata, token string, notice string, errMsg string, generatedPassword string, deleted bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	baseURL := requestBaseURL(r)
	publicURL := strings.TrimRight(baseURL, "/") + "/" + meta.ID
	manageURL := publicURL + "?manage=" + token
	policyKind, customPolicyValue := splitManagePolicy(meta.DataPolicy)

	_ = a.managePage.Execute(w, managePageData{
		ID:                meta.ID,
		Filename:          meta.Filename,
		PublicURL:         publicURL,
		ManageURL:         manageURL,
		CreatedAt:         meta.CreatedAt,
		ExpiresAt:         meta.ExpiresAt,
		DataPolicy:        meta.DataPolicy,
		PasswordProtected: meta.PasswordHash != "",
		Notice:            notice,
		Error:             errMsg,
		GeneratedPassword: generatedPassword,
		Deleted:           deleted,
		PolicyKind:        policyKind,
		CustomPolicyValue: customPolicyValue,
	})
}

func parseManagePolicyForm(r *http.Request) (string, error) {
	policyKind := strings.ToLower(strings.TrimSpace(r.FormValue("data_policy")))
	if policyKind == "custom" {
		customValue := strings.ToLower(strings.TrimSpace(r.FormValue("custom_policy")))
		if customValue == "" {
			return "", pastebox.ErrInvalidPolicy
		}
		if _, err := pastebox.ParseDataPolicy(customValue); err != nil {
			return "", pastebox.ErrInvalidPolicy
		}
		return customValue, nil
	}

	if _, err := pastebox.ParseDataPolicy(policyKind); err != nil {
		return "", pastebox.ErrInvalidPolicy
	}
	return policyKind, nil
}

func splitManagePolicy(policy string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", "temporary":
		return "temporary", ""
	case "permanent":
		return "permanent", ""
	case "once":
		return "once", ""
	default:
		return "custom", strings.ToLower(strings.TrimSpace(policy))
	}
}

func (a *app) manageErrorMessage(err error) string {
	switch {
	case errors.Is(err, pastebox.ErrInvalidPassword):
		return a.i18n.T("manage_error_invalid_password")
	case errors.Is(err, pastebox.ErrInvalidManageToken):
		return a.i18n.T("manage_error_invalid_token")
	case errors.Is(err, pastebox.ErrAlreadyProtected):
		return a.i18n.T("manage_error_already_protected")
	case errors.Is(err, pastebox.ErrInvalidPolicy):
		return a.i18n.T("manage_error_invalid_policy")
	default:
		return a.i18n.T("manage_error_generic")
	}
}
