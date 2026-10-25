package main

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *app) adminSetupHandler(w http.ResponseWriter, r *http.Request) {
	exists, err := a.store.AdminExists()
	if err != nil {
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return
	}

	if exists {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodGet {
		a.renderAdminForm(w, a.i18n.T("admin_setup_title"), "/admin/setup", "", a.i18n.T("admin_create_button"))
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !verifyAdminCSRFToken(w, r) {
		logEvent("admin.csrf_validation_failed", map[string]any{
			"path":   r.URL.Path,
			"remote": r.RemoteAddr,
		})
		a.renderAdminForm(w, a.i18n.T("admin_setup_title"), "/admin/setup", a.i18n.T("admin_error_invalid_form"), a.i18n.T("admin_create_button"))
		return
	}

	if err := r.ParseForm(); err != nil {
		a.renderAdminForm(w, a.i18n.T("admin_setup_title"), "/admin/setup", a.i18n.T("admin_error_invalid_form"), a.i18n.T("admin_create_button"))
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	setupToken := strings.TrimSpace(r.FormValue("setup_token"))

	if subtle.ConstantTimeCompare([]byte(setupToken), []byte(a.adminSetupToken)) != 1 {
		a.renderAdminForm(w, a.i18n.T("admin_setup_title"), "/admin/setup", a.i18n.T("admin_error_invalid_setup_token"), a.i18n.T("admin_create_button"))
		return
	}

	if err := a.store.CreateAdmin(username, password); err != nil {
		a.renderAdminForm(w, a.i18n.T("admin_setup_title"), "/admin/setup", err.Error(), a.i18n.T("admin_create_button"))
		return
	}

	logEvent("admin.created", map[string]any{
		"remote":   r.RemoteAddr,
		"username": username,
	})
	a.adminSetupToken = ""

	token, err := a.store.CreateAdminSession()
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	setAdminCookie(w, token)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *app) adminLoginHandler(w http.ResponseWriter, r *http.Request) {
	exists, err := a.store.AdminExists()
	if err != nil {
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return
	}

	if !exists {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodGet {
		a.renderAdminForm(w, a.i18n.T("admin_login_title"), "/admin/login", "", a.i18n.T("admin_login_button"))
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !verifyAdminCSRFToken(w, r) {
		logEvent("admin.csrf_validation_failed", map[string]any{
			"path":   r.URL.Path,
			"remote": r.RemoteAddr,
		})
		a.renderAdminForm(w, a.i18n.T("admin_login_title"), "/admin/login", a.i18n.T("admin_error_invalid_form"), a.i18n.T("admin_login_button"))
		return
	}

	if err := r.ParseForm(); err != nil {
		a.renderAdminForm(w, a.i18n.T("admin_login_title"), "/admin/login", a.i18n.T("admin_error_invalid_form"), a.i18n.T("admin_login_button"))
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	clientIP := requestClientIP(r)
	usernameKey := "admin_login:user:" + strings.ToLower(strings.TrimSpace(username))
	ipKey := "admin_login:ip:" + clientIP
	pairKey := "admin_login:pair:" + clientIP + ":" + strings.ToLower(strings.TrimSpace(username))
	if retryAfter, blocked := a.authLimiter.retryAfterAny([]string{ipKey, usernameKey, pairKey}, time.Now()); blocked {
		logEvent("admin.login_rate_limited", map[string]any{
			"client_ip":   clientIP,
			"remote":      r.RemoteAddr,
			"retry_after": retryAfter.Round(time.Second),
		})
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		http.Error(w, "too many failed attempts. try again later", http.StatusTooManyRequests)
		return
	}

	ok, err := a.store.AuthenticateAdmin(username, password)
	if err != nil {
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return
	}

	if !ok {
		a.authLimiter.recordFailureMany([]string{ipKey, usernameKey, pairKey}, time.Now())
		logEvent("admin.login_failed", map[string]any{
			"remote":   r.RemoteAddr,
			"username": username,
		})
		a.renderAdminForm(w, a.i18n.T("admin_login_title"), "/admin/login", a.i18n.T("admin_error_invalid_credentials"), a.i18n.T("admin_login_button"))
		return
	}
	a.authLimiter.clearMany([]string{ipKey, usernameKey, pairKey})

	token, err := a.store.CreateAdminSession()
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	logEvent("admin.login_succeeded", map[string]any{
		"remote":   r.RemoteAddr,
		"username": username,
	})

	setAdminCookie(w, token)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *app) adminLogoutHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("pastebox_admin")
	if err == nil {
		_ = a.store.DeleteAdminSession(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "pastebox_admin",
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     adminSkipSingleDeleteConfirmCookieName,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (a *app) adminResetHandler(w http.ResponseWriter, r *http.Request) {
	exists, err := a.store.AdminExists()
	if err != nil {
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}

	if strings.TrimSpace(a.adminResetToken) == "" {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodGet {
		a.renderAdminResetForm(w, "")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !verifyAdminCSRFToken(w, r) {
		logEvent("admin.csrf_validation_failed", map[string]any{
			"path":   r.URL.Path,
			"remote": r.RemoteAddr,
		})
		a.renderAdminResetForm(w, a.i18n.T("admin_error_invalid_form"))
		return
	}

	if err := r.ParseForm(); err != nil {
		a.renderAdminResetForm(w, a.i18n.T("admin_error_invalid_form"))
		return
	}

	resetToken := strings.TrimSpace(r.FormValue("reset_token"))
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")
	clientIP := requestClientIP(r)
	limitKey := "admin_reset:" + clientIP
	if retryAfter, blocked := a.authLimiter.retryAfter(limitKey, time.Now()); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		http.Error(w, "too many failed attempts. try again later", http.StatusTooManyRequests)
		return
	}

	if subtle.ConstantTimeCompare([]byte(resetToken), []byte(a.adminResetToken)) != 1 {
		a.authLimiter.recordFailure(limitKey, time.Now())
		a.renderAdminResetForm(w, a.i18n.T("admin_reset_error_invalid_token"))
		return
	}

	if strings.TrimSpace(password) == "" {
		a.renderAdminResetForm(w, a.i18n.T("admin_reset_error_password_required"))
		return
	}
	if password != passwordConfirm {
		a.renderAdminResetForm(w, a.i18n.T("admin_reset_error_password_mismatch"))
		return
	}

	if err := a.store.ForceResetAdminPassword(password); err != nil {
		http.Error(w, "failed to reset password", http.StatusInternalServerError)
		return
	}
	a.authLimiter.clear(limitKey)

	logEvent("admin.password_reset", map[string]any{
		"remote": r.RemoteAddr,
	})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (a *app) renderAdminForm(w http.ResponseWriter, title string, action string, errorMessage string, button string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	description := a.i18n.T("admin_login_description")
	if action == "/admin/setup" {
		description = a.i18n.T("admin_setup_description")
	}

	_ = a.adminForm.Execute(w, map[string]any{
		"Title":             title,
		"Action":            action,
		"Error":             errorMessage,
		"Button":            button,
		"Description":       description,
		"RequireSetupToken": action == "/admin/setup",
		"CSRFToken":         issueAdminCSRFToken(w, nil),
	})
}

func (a *app) renderAdminResetForm(w http.ResponseWriter, errorMessage string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.adminReset.Execute(w, map[string]any{
		"Title":       a.i18n.T("admin_reset_title"),
		"Error":       errorMessage,
		"Action":      "/admin/reset",
		"Button":      a.i18n.T("admin_reset_button"),
		"Description": a.i18n.T("admin_reset_description"),
		"CSRFToken":   issueAdminCSRFToken(w, nil),
	})
}
