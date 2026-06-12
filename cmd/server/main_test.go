package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestViewHandlerHeadDoesNotConsumeOncePaste(t *testing.T) {
	app := newTestApp(t)

	meta, _, _, _, err := app.store.Create(strings.NewReader("head request"), "head.txt", "text/plain; charset=utf-8", false, false, true, "head1")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodHead, "/"+meta.ID+"?format=raw", nil)
	req.Header.Set("User-Agent", "curl/8.0.0")
	rr := httptest.NewRecorder()

	app.viewHandler(rr, req, meta.ID)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	entry, err := app.store.Open(meta.ID, "")
	if err != nil {
		t.Fatalf("Open failed after HEAD: %v", err)
	}
	_ = entry.File.Close()
}

func TestViewHandlerGetConsumesOncePaste(t *testing.T) {
	app := newTestApp(t)

	meta, _, _, _, err := app.store.Create(strings.NewReader("get request"), "get.txt", "text/plain; charset=utf-8", false, false, true, "get1")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/"+meta.ID+"?format=raw", nil)
	req.Header.Set("User-Agent", "curl/8.0.0")
	rr := httptest.NewRecorder()

	app.viewHandler(rr, req, meta.ID)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "get request" {
		t.Fatalf("unexpected body %q", body)
	}

	entry, err := app.store.Open(meta.ID, "")
	if !errors.Is(err, pastebox.ErrNotFound) {
		if entry != nil && entry.File != nil {
			_ = entry.File.Close()
		}
		t.Fatalf("expected ErrNotFound after GET, got entry=%v err=%v", entry != nil, err)
	}
}
