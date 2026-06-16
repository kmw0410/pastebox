package main

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	pastebox "pastebox/internal"
)

func (a *app) adminHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/admin", "/admin/":
		a.adminIndexHandler(w, r)
	case "/admin/setup":
		a.adminSetupHandler(w, r)
	case "/admin/login":
		a.adminLoginHandler(w, r)
	case "/admin/reset":
		a.adminResetHandler(w, r)
	case "/admin/logout":
		a.adminLogoutHandler(w, r)
	case "/admin/delete":
		a.adminDeleteHandler(w, r)
	case "/admin/delete-all":
		a.adminDeleteAllHandler(w, r)
	case "/admin/uploads":
		a.adminUploadsHandler(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (a *app) adminIndexHandler(w http.ResponseWriter, r *http.Request) {
	exists, err := a.store.AdminExists()
	if err != nil {
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return
	}

	if !exists {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}

	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}

	items, err := a.store.ListPastes()
	if err != nil {
		http.Error(w, "failed to list pastes", http.StatusInternalServerError)
		return
	}
	items = localizeAdminPasteItems(items, time.Local)

	uploadsDisabled, err := a.store.UploadsDisabled()
	if err != nil {
		http.Error(w, "failed to read upload status", http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"Items":           items,
		"Stats":           buildAdminStats(items),
		"BaseURL":         requestBaseURL(r),
		"UploadsDisabled": uploadsDisabled,
		"Notice":          popAdminFlash(w, r),
		"CSRFToken":       issueAdminCSRFToken(w, r),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.adminList.Execute(w, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

const adminFlashCookieName = "pastebox_admin_flash"

func setAdminFlash(w http.ResponseWriter, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminFlashCookieName,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(message)),
		Path:     "/admin",
		MaxAge:   60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func popAdminFlash(w http.ResponseWriter, r *http.Request) string {
	cookie, err := r.Cookie(adminFlashCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return ""
	}

	http.SetCookie(w, &http.Cookie{
		Name:     adminFlashCookieName,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	message, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return ""
	}

	return string(message)
}

type adminStats struct {
	Total          int
	Temporary      int
	Permanent      int
	Protected      int
	Expiring24h    int
	Expired        int
	TotalSizeBytes int64
	TotalSize      string
}

func buildAdminStats(items []pastebox.AdminPasteItem) adminStats {
	now := time.Now().UTC()
	stats := adminStats{
		Total: len(items),
	}

	for _, item := range items {
		stats.TotalSizeBytes += item.Size

		if strings.EqualFold(item.DataPolicy, "permanent") {
			stats.Permanent++
		} else {
			stats.Temporary++
		}

		if item.Protected {
			stats.Protected++
		}

		if !item.ExpiresAt.IsZero() {
			if now.After(item.ExpiresAt) {
				stats.Expired++
			} else if item.ExpiresAt.Sub(now) <= 24*time.Hour {
				stats.Expiring24h++
			}
		}
	}

	stats.TotalSize = formatBytes(stats.TotalSizeBytes)
	return stats
}

func localizeAdminPasteItems(items []pastebox.AdminPasteItem, loc *time.Location) []pastebox.AdminPasteItem {
	if loc == nil {
		return items
	}

	out := make([]pastebox.AdminPasteItem, len(items))
	copy(out, items)

	for i := range out {
		out[i].CreatedAt = out[i].CreatedAt.In(loc)
		if !out[i].ExpiresAt.IsZero() {
			out[i].ExpiresAt = out[i].ExpiresAt.In(loc)
		}
	}

	return out
}

func formatBytes(size int64) string {
	const unit = 1024

	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div := int64(unit)
	exp := 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

func (a *app) logAdminAction(action string, actor adminActor, outcome string, fields map[string]any) {
	payload := map[string]any{
		"action":    action,
		"client_ip": actor.ClientIP,
		"outcome":   outcome,
		"remote":    actor.Remote,
		"username":  actor.Username,
	}
	for key, value := range fields {
		payload[key] = value
	}

	logEvent("admin.audit", payload)
}

func summarizeAdminIDs(ids []string) string {
	trimmed := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		trimmed = append(trimmed, id)
	}

	if len(trimmed) == 0 {
		return ""
	}

	const maxIDs = 20
	if len(trimmed) <= maxIDs {
		return strings.Join(trimmed, ",")
	}

	return fmt.Sprintf("%s,...(+%d more)", strings.Join(trimmed[:maxIDs], ","), len(trimmed)-maxIDs)
}

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

func (a *app) adminUploadsHandler(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !verifyAdminCSRFToken(w, r) {
		a.logAdminAction("uploads.set", actor, "denied", map[string]any{
			"path":   r.URL.Path,
			"reason": "invalid_csrf",
		})
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		a.logAdminAction("uploads.set", actor, "failure", map[string]any{
			"error": err,
		})
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	disabled := r.FormValue("disabled") == "true"

	if err := a.store.SetUploadsDisabled(disabled); err != nil {
		http.Error(w, "failed to update upload status", http.StatusInternalServerError)
		return
	}

	a.logAdminAction("uploads.set", actor, "success", map[string]any{
		"disabled": disabled,
	})

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *app) adminDeleteAllHandler(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !verifyAdminCSRFToken(w, r) {
		a.logAdminAction("pastes.delete_all", actor, "denied", map[string]any{
			"path":   r.URL.Path,
			"reason": "invalid_csrf",
		})
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	count, err := a.store.AdminDeleteAll()
	if err != nil {
		a.logAdminAction("pastes.delete_all", actor, "failure", map[string]any{
			"deleted_count": count,
			"error":         err,
		})
		http.Error(w, "delete all failed", http.StatusInternalServerError)
		return
	}

	a.logAdminAction("pastes.delete_all", actor, "success", map[string]any{
		"deleted_count": count,
	})

	setAdminFlash(w, fmt.Sprintf(a.i18n.T("admin_flash_delete_all"), count))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *app) adminDeleteHandler(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !verifyAdminCSRFToken(w, r) {
		a.logAdminAction("paste.delete", actor, "denied", map[string]any{
			"path":   r.URL.Path,
			"reason": "invalid_csrf",
		})
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		a.logAdminAction("paste.delete", actor, "failure", map[string]any{
			"error": err,
		})
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	ids := r.Form["ids"]
	if len(ids) > 0 {
		deleted := 0
		selectedIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			selectedIDs = append(selectedIDs, id)
			if err := a.store.AdminDelete(id); err != nil {
				a.logAdminAction("pastes.delete_selected", actor, "failure", map[string]any{
					"deleted_count": deleted,
					"error":         err,
					"ids":           summarizeAdminIDs(selectedIDs),
				})
				http.Error(w, "delete failed", http.StatusBadRequest)
				return
			}
			deleted++
		}
		if deleted > 0 {
			a.logAdminAction("pastes.delete_selected", actor, "success", map[string]any{
				"deleted_count": deleted,
				"ids":           summarizeAdminIDs(selectedIDs),
			})
			setAdminFlash(w, fmt.Sprintf(a.i18n.T("admin_flash_delete_all"), deleted))
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	id := r.FormValue("id")
	if err := a.store.AdminDelete(id); err != nil {
		a.logAdminAction("paste.delete", actor, "failure", map[string]any{
			"error": err,
			"id":    strings.TrimSpace(id),
		})
		http.Error(w, "delete failed", http.StatusBadRequest)
		return
	}

	a.logAdminAction("paste.delete", actor, "success", map[string]any{
		"id": id,
	})

	setAdminFlash(w, fmt.Sprintf(a.i18n.T("admin_flash_delete_one"), id))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
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
