package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
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
	now := time.Now()
	filters := adminPasteFiltersFromRequest(r)
	filteredItems := filterAdminPasteItems(items, filters, now.UTC())
	sorting := adminPasteSortFromRequest(r)
	sortAdminPasteItems(filteredItems, sorting)
	pagination := adminPaginationFromRequest(r, len(filteredItems))
	pagedItems := paginateAdminPasteItems(filteredItems, pagination)

	uploadsDisabled, err := a.store.UploadsDisabled()
	if err != nil {
		http.Error(w, "failed to read upload status", http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"Items":           buildAdminPasteViews(pagedItems, now, a.i18n),
		"Stats":           buildAdminStats(items),
		"Filters":         filters,
		"Sorting":         sorting,
		"FilteredCount":   len(filteredItems),
		"Pagination":      pagination,
		"BaseURL":         requestBaseURL(r),
		"StorageBackend":  a.store.StorageBackend,
		"UploadsDisabled": uploadsDisabled,
		"Notice":          popAdminFlash(w, r),
		"CSRFToken":       issueAdminCSRFToken(w, r),
		"Release":         a.releaseChecker.Check(r.Context()),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.adminList.Execute(w, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

const (
	adminFlashCookieName                   = "pastebox_admin_flash"
	adminSkipSingleDeleteConfirmCookieName = "pastebox_admin_skip_single_delete_confirm"
)

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
	a.notifyDiscordPastesDeleted(count, "admin delete all")

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
			a.notifyDiscordPastesDeleted(deleted, "admin bulk delete")
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
	a.notifyDiscordPasteDeleted(id, "admin")

	setAdminFlash(w, fmt.Sprintf(a.i18n.T("admin_flash_delete_one"), id))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
