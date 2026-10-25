package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	pastebox "pastebox/internal"
)

func newTestApp(t *testing.T) *app {
	t.Helper()

	store, err := pastebox.NewStore(t.TempDir(), 24*time.Hour)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	return &app{store: store}
}

func mustParsePolicy(t *testing.T, value string) pastebox.DataPolicy {
	t.Helper()

	policy, err := pastebox.ParseDataPolicy(value)
	if err != nil {
		t.Fatalf("ParseDataPolicy(%q) failed: %v", value, err)
	}
	return policy
}

func TestAuthAttemptLimiterCapsDistinctKeys(t *testing.T) {
	limiter := newAuthAttemptLimiter(time.Minute, 3)
	now := time.Now()
	for i := 0; i < maxAuthLimiterEntries+25; i++ {
		limiter.recordFailure(strconv.Itoa(i), now)
	}
	if got := len(limiter.entries); got != maxAuthLimiterEntries {
		t.Fatalf("limiter entries = %d, want %d", got, maxAuthLimiterEntries)
	}
}

func TestStaticFileHandlerSetsCacheAndSecurityHeaders(t *testing.T) {
	handler := staticFileHandler("/js/", "../../templates/js", "public, max-age=31536000, immutable")
	req := httptest.NewRequest(http.MethodGet, "/js/kdl.min.js", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}

func TestCloneHandlerAcceptsCustomPasswordHeader(t *testing.T) {
	app := newTestApp(t)
	source, _, _, _, err := app.store.Create(strings.NewReader("clone body"), "source.txt", "text/plain", false, mustParsePolicy(t, "temporary"), "sourcepw")
	if err != nil {
		t.Fatalf("Create source failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/"+source.ID+"?format=json", nil)
	req.Header.Set("code", "clonepw")
	req.Header.Set("password", "custom-secret")
	rr := httptest.NewRecorder()

	app.cloneHandler(rr, req, source.ID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "custom-secret") {
		t.Fatalf("response exposed supplied password: %q", rr.Body.String())
	}
	var response uploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.PasswordProtected {
		t.Fatalf("password_protected = false, body=%q", rr.Body.String())
	}
	entry, err := app.store.Open("clonepw", "custom-secret")
	if err != nil {
		t.Fatalf("Open clone failed: %v", err)
	}
	_ = entry.File.Close()
}

func TestHealthHandlerOK(t *testing.T) {
	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	app.healthHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "ok\n" {
		t.Fatalf("unexpected body %q", body)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestHealthHandlerServiceUnavailableOnStoreFailure(t *testing.T) {
	app := &app{store: &pastebox.Store{}}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	app.healthHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "unhealthy") {
		t.Fatalf("unexpected body %q", rr.Body.String())
	}
}

func responseLineValue(body string, key string) string {
	prefix := key + ": "
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
