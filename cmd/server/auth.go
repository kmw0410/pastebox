package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

func (a *app) requireAdmin(w http.ResponseWriter, r *http.Request) (adminActor, bool) {
	actor := adminActor{
		ClientIP: requestClientIP(r),
		Remote:   r.RemoteAddr,
	}

	cookie, err := r.Cookie("pastebox_admin")
	if err != nil {
		a.logAdminAction("admin.access", actor, "denied", map[string]any{
			"path":   r.URL.Path,
			"reason": "missing_session_cookie",
		})
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return actor, false
	}

	ok, err := a.store.ValidAdminSession(cookie.Value)
	if err != nil {
		a.logAdminAction("admin.access", actor, "failure", map[string]any{
			"error": err,
			"path":  r.URL.Path,
		})
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return actor, false
	}
	if !ok {
		a.logAdminAction("admin.access", actor, "denied", map[string]any{
			"path":   r.URL.Path,
			"reason": "invalid_session",
		})
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return actor, false
	}

	username, err := a.store.AdminUsername()
	if err != nil {
		a.logAdminAction("admin.access", actor, "failure", map[string]any{
			"error": err,
			"path":  r.URL.Path,
		})
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return actor, false
	}

	actor.Username = username
	return actor, true
}

func setAdminCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "pastebox_admin",
		Value:    token,
		Path:     "/admin",
		MaxAge:   86400,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

type authAttemptLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	limit   int
	entries map[string]*authAttemptEntry
}

type authAttemptEntry struct {
	attempts []time.Time
	lastSeen time.Time
}

const maxAuthLimiterEntries = 10_000

func newAuthAttemptLimiter(window time.Duration, limit int) *authAttemptLimiter {
	return &authAttemptLimiter{
		window:  window,
		limit:   limit,
		entries: make(map[string]*authAttemptEntry),
	}
}

func (l *authAttemptLimiter) retryAfter(key string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	attempts := l.pruneLocked(key, now)
	if len(attempts) < l.limit {
		return 0, false
	}

	retryAfter := attempts[0].Add(l.window).Sub(now)
	if retryAfter < 0 {
		return 0, false
	}
	return retryAfter, true
}

func (l *authAttemptLimiter) recordFailure(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	attempts := l.pruneLocked(key, now)
	if _, exists := l.entries[key]; !exists && len(l.entries) >= maxAuthLimiterEntries {
		l.pruneAllLocked(now)
		if len(l.entries) >= maxAuthLimiterEntries {
			l.evictOldestLocked()
		}
	}
	l.entries[key] = &authAttemptEntry{attempts: append(attempts, now), lastSeen: now}
}

func (l *authAttemptLimiter) retryAfterAny(keys []string, now time.Time) (time.Duration, bool) {
	minRetry := time.Duration(0)
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		retryAfter, blocked := l.retryAfter(key, now)
		if !blocked {
			continue
		}
		if minRetry == 0 || retryAfter < minRetry {
			minRetry = retryAfter
		}
	}
	if minRetry == 0 {
		return 0, false
	}
	return minRetry, true
}

func (l *authAttemptLimiter) recordFailureMany(keys []string, now time.Time) {
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		l.recordFailure(key, now)
	}
}

func (l *authAttemptLimiter) clearMany(keys []string) {
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		l.clear(key)
	}
}

func (l *authAttemptLimiter) clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

func (l *authAttemptLimiter) pruneLocked(key string, now time.Time) []time.Time {
	entry := l.entries[key]
	if entry == nil || len(entry.attempts) == 0 {
		return nil
	}

	cutoff := now.Add(-l.window)
	keep := entry.attempts[:0]
	for _, at := range entry.attempts {
		if at.After(cutoff) {
			keep = append(keep, at)
		}
	}

	if len(keep) == 0 {
		delete(l.entries, key)
		return nil
	}

	entry.attempts = keep
	entry.lastSeen = now
	return keep
}

func (l *authAttemptLimiter) pruneAllLocked(now time.Time) {
	cutoff := now.Add(-l.window)
	for key, entry := range l.entries {
		if entry == nil || len(entry.attempts) == 0 || !entry.attempts[len(entry.attempts)-1].After(cutoff) {
			delete(l.entries, key)
		}
	}
}

func (l *authAttemptLimiter) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time
	for key, entry := range l.entries {
		if oldestKey == "" || entry.lastSeen.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.lastSeen
		}
	}
	if oldestKey != "" {
		delete(l.entries, oldestKey)
	}
}

func requestClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		host = strings.TrimSpace(host)
		if host != "" {
			return host
		}
	}

	remote := strings.TrimSpace(r.RemoteAddr)
	if remote == "" {
		return "unknown"
	}
	return remote
}

const adminCSRFCookieName = "pastebox_admin_csrf"

func issueAdminCSRFToken(w http.ResponseWriter, r *http.Request) string {
	if r != nil {
		if cookie, err := r.Cookie(adminCSRFCookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
			return cookie.Value
		}
	}
	token, err := randomCSRFToken()
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCSRFCookieName,
		Value:    token,
		Path:     "/admin",
		MaxAge:   86400,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return token
}

func randomCSRFToken() (string, error) {
	buf := make([]byte, 36)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func verifyAdminCSRFToken(w http.ResponseWriter, r *http.Request) bool {
	cookie, err := r.Cookie(adminCSRFCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return false
	}
	formToken := strings.TrimSpace(r.FormValue("csrf_token"))
	if formToken == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(formToken)) != 1 {
		return false
	}
	issueAdminCSRFToken(w, r)
	return true
}
