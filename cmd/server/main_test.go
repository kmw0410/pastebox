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

func TestSummarizeAdminIDs(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		want string
	}{
		{
			name: "empty",
			ids:  nil,
			want: "",
		},
		{
			name: "trim and join",
			ids:  []string{" one ", "", "two"},
			want: "one,two",
		},
		{
			name: "truncate long list",
			ids: []string{
				"1", "2", "3", "4", "5", "6", "7", "8", "9", "10",
				"11", "12", "13", "14", "15", "16", "17", "18", "19", "20",
				"21", "22",
			},
			want: "1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,...(+2 more)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeAdminIDs(tt.ids); got != tt.want {
				t.Fatalf("summarizeAdminIDs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeTextContentType(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		contentType string
		want        string
	}{
		{name: "dockerfile by name", filename: "Dockerfile", want: "text/x-dockerfile; charset=utf-8"},
		{name: "sql by ext", filename: "schema.sql", want: "application/sql; charset=utf-8"},
		{name: "nginx by name", filename: "nginx.conf", want: "text/x-nginx-conf; charset=utf-8"},
		{name: "lua by ext", filename: "init.lua", want: "text/x-lua; charset=utf-8"},
		{name: "toml by ext", filename: "pyproject.toml", want: "application/toml; charset=utf-8"},
		{name: "bash by ext", filename: "deploy.sh", want: "text/x-shellscript; charset=utf-8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeTextContentType(tt.filename, tt.contentType); got != tt.want {
				t.Fatalf("normalizeTextContentType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSyntaxLanguage(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		contentType string
		want        string
	}{
		{name: "dockerfile by name", filename: "Dockerfile", contentType: "text/plain; charset=utf-8", want: "dockerfile"},
		{name: "sql by content type", filename: "query.sql", contentType: "application/sql; charset=utf-8", want: "sql"},
		{name: "nginx by name", filename: "nginx.conf", contentType: "text/plain; charset=utf-8", want: "nginx"},
		{name: "lua by content type", filename: "init.lua", contentType: "text/x-lua; charset=utf-8", want: "lua"},
		{name: "toml by content type", filename: "Cargo.toml", contentType: "application/toml; charset=utf-8", want: "toml"},
		{name: "bash by content type", filename: "entrypoint.sh", contentType: "text/x-shellscript; charset=utf-8", want: "bash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := syntaxLanguage(tt.filename, tt.contentType); got != tt.want {
				t.Fatalf("syntaxLanguage() = %q, want %q", got, tt.want)
			}
		})
	}
}
