package main

import (
	"bytes"
	"errors"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestPrepareTextUploadReaderStreamsAfterSample(t *testing.T) {
	source := strings.NewReader("abcdefghijkl")
	reader, sample, err := prepareTextUploadReader(source, 12, 4)
	if err != nil {
		t.Fatalf("prepareTextUploadReader failed: %v", err)
	}
	if got := string(sample); got != "abcd" {
		t.Fatalf("sample = %q, want %q", got, "abcd")
	}
	if remaining := source.Len(); remaining != 8 {
		t.Fatalf("source bytes consumed before storage = %d, want 4", 12-remaining)
	}

	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("stream read failed: %v", err)
	}
	if got := string(content); got != "abcdefghijkl" {
		t.Fatalf("content = %q", got)
	}
}

func TestPrepareTextUploadReaderEnforcesLimit(t *testing.T) {
	reader, _, err := prepareTextUploadReader(strings.NewReader("abcdef"), 5, 2)
	if err != nil {
		t.Fatalf("prepareTextUploadReader failed: %v", err)
	}

	content, err := io.ReadAll(reader)
	if !errors.Is(err, errUploadTooLarge) {
		t.Fatalf("read error = %v, want errUploadTooLarge", err)
	}
	if got := string(content); got != "abcde" {
		t.Fatalf("content before limit = %q, want %q", got, "abcde")
	}
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

func TestViewHandlerHeadDoesNotConsumeOncePaste(t *testing.T) {
	app := newTestApp(t)

	meta, _, _, _, err := app.store.Create(strings.NewReader("head request"), "head.txt", "text/plain; charset=utf-8", false, mustParsePolicy(t, "once"), "head1")
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

	meta, _, _, _, err := app.store.Create(strings.NewReader("get request"), "get.txt", "text/plain; charset=utf-8", false, mustParsePolicy(t, "once"), "get1")
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

func TestViewHandlerLimitsHTMLRenderingByPasteSize(t *testing.T) {
	tests := []struct {
		name        string
		size        int64
		contentType string
		wantBody    string
	}{
		{
			name:        "renders paste at HTML view limit",
			size:        maxHTMLViewSize,
			contentType: "text/html; charset=utf-8",
			wantBody:    "html-view",
		},
		{
			name:        "streams paste above HTML view limit",
			size:        maxHTMLViewSize + 1,
			contentType: "text/plain; charset=utf-8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestApp(t)
			app.paste = template.Must(template.New("paste").Parse("html-view"))
			content := bytes.Repeat([]byte("a"), int(tt.size))
			meta, _, _, _, err := app.store.Create(bytes.NewReader(content), "large.txt", "text/plain; charset=utf-8", false, mustParsePolicy(t, "temporary"), "large1")
			if err != nil {
				t.Fatalf("Create failed: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/"+meta.ID, nil)
			req.Header.Set("Accept", "text/html")
			rr := httptest.NewRecorder()

			app.viewHandler(rr, req, meta.ID)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}
			if got := rr.Header().Get("Content-Type"); got != tt.contentType {
				t.Fatalf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if tt.wantBody != "" {
				if got := rr.Body.String(); got != tt.wantBody {
					t.Fatalf("body = %q, want %q", got, tt.wantBody)
				}
				return
			}
			if got := int64(rr.Body.Len()); got != tt.size {
				t.Fatalf("streamed body size = %d, want %d", got, tt.size)
			}
		})
	}
}

func TestPasteOpenGraphDescription(t *testing.T) {
	tests := []struct {
		name string
		meta pastebox.Metadata
		want string
	}{
		{name: "public filename", meta: pastebox.Metadata{Filename: "server.log"}, want: "server.log"},
		{name: "public without filename", meta: pastebox.Metadata{}, want: "Shared text paste"},
		{name: "protected hides filename", meta: pastebox.Metadata{Filename: "secret.env", Label: "production", PasswordHash: "hash"}, want: "Password-protected paste"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pasteOpenGraphDescription(tt.meta); got != tt.want {
				t.Fatalf("description = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUploadHandlerCustomDataPolicyDuration(t *testing.T) {
	app := newTestApp(t)
	loc := time.FixedZone("Test/KST", 9*60*60)
	originalLocal := time.Local
	time.Local = loc
	t.Cleanup(func() {
		time.Local = originalLocal
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "custom.log")
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := part.Write([]byte("custom ttl\n")); err != nil {
		t.Fatalf("part.Write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close failed: %v", err)
	}

	before := time.Now().UTC()
	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("data-policy", "1h")
	rr := httptest.NewRecorder()

	app.uploadHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	expiresText := responseLineValue(rr.Body.String(), "expires")
	if expiresText == "" {
		t.Fatalf("missing expires line in response %q", rr.Body.String())
	}
	expiresAt, err := time.Parse(time.RFC3339, expiresText)
	if err != nil {
		t.Fatalf("invalid expires value %q: %v", expiresText, err)
	}
	if !strings.HasSuffix(expiresText, "+09:00") {
		t.Fatalf("expires = %q, want server local timezone offset +09:00", expiresText)
	}

	after := time.Now().UTC()
	minExpires := before.Add(time.Hour).Truncate(time.Second)
	maxExpires := after.Add(time.Hour).Truncate(time.Second).Add(time.Second)
	if expiresAt.Before(minExpires) || expiresAt.After(maxExpires) {
		t.Fatalf("expires = %v, want between %v and %v", expiresAt, minExpires, maxExpires)
	}
}

func TestUploadHandlerStoresLabel(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("labeled paste"))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("label", " production log ")
	req.Header.Set("code", "labeled")
	rr := httptest.NewRecorder()

	app.uploadHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rr.Code, rr.Body.String())
	}
	entry, err := app.store.Open("labeled", "")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer entry.File.Close()
	if entry.Meta.Label != "production log" {
		t.Fatalf("label = %q, want production log", entry.Meta.Label)
	}
}

func TestUploadHandlerAcceptsCustomPassword(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/?format=json", strings.NewReader("protected paste"))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("code", "prompted")
	req.Header.Set("new-paste-password", "custom-secret")
	rr := httptest.NewRecorder()

	app.uploadHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "custom-secret") {
		t.Fatalf("response exposed supplied password: %q", rr.Body.String())
	}
	entry, err := app.store.Open("prompted", "custom-secret")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
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

func TestFilterAdminPasteItems(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	items := []pastebox.AdminPasteItem{
		{
			ID:         "build-log",
			Filename:   "server.log",
			DataPolicy: "temporary",
			ExpiresAt:  now.Add(48 * time.Hour),
			Protected:  false,
		},
		{
			ID:         "secret",
			Filename:   "config.yaml",
			DataPolicy: "permanent",
			Protected:  true,
		},
		{
			ID:         "once1",
			Filename:   "readme.md",
			DataPolicy: "once",
			ExpiresAt:  now.Add(30 * time.Minute),
			Protected:  false,
		},
		{
			ID:         "custom1",
			Filename:   "trace.txt",
			DataPolicy: "12h",
			ExpiresAt:  now.Add(-time.Hour),
			Protected:  true,
		},
	}

	tests := []struct {
		name    string
		filters adminPasteFilters
		want    []string
	}{
		{
			name:    "query matches code and filename",
			filters: adminPasteFilters{Query: "LOG"},
			want:    []string{"build-log"},
		},
		{
			name:    "custom policy",
			filters: adminPasteFilters{Policy: "custom"},
			want:    []string{"custom1"},
		},
		{
			name:    "protected",
			filters: adminPasteFilters{Protected: "yes"},
			want:    []string{"secret", "custom1"},
		},
		{
			name:    "expiring",
			filters: adminPasteFilters{Status: "expiring"},
			want:    []string{"once1"},
		},
		{
			name:    "no expiration",
			filters: adminPasteFilters{Status: "no-expiration"},
			want:    []string{"secret"},
		},
		{
			name:    "combined filters",
			filters: adminPasteFilters{Policy: "custom", Protected: "yes", Status: "expired"},
			want:    []string{"custom1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterAdminPasteItems(items, tt.filters, now)
			gotIDs := make([]string, 0, len(got))
			for _, item := range got {
				gotIDs = append(gotIDs, item.ID)
			}
			if strings.Join(gotIDs, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("filterAdminPasteItems() = %v, want %v", gotIDs, tt.want)
			}
		})
	}
}

func TestNewAppGeneratesSetupTokenOnlyWithoutAdmin(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	store, err := pastebox.NewStore(t.TempDir(), 24*time.Hour)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	i18n := loadLocalizer("en")

	appWithoutAdmin, err := newApp(store, i18n, "")
	if err != nil {
		t.Fatalf("newApp without admin failed: %v", err)
	}
	if appWithoutAdmin.adminSetupToken == "" {
		t.Fatalf("expected setup token when admin does not exist")
	}

	if err := store.CreateAdmin("admin", "password123"); err != nil {
		t.Fatalf("CreateAdmin failed: %v", err)
	}

	appWithAdmin, err := newApp(store, i18n, "")
	if err != nil {
		t.Fatalf("newApp with admin failed: %v", err)
	}
	if appWithAdmin.adminSetupToken != "" {
		t.Fatalf("expected empty setup token when admin exists, got %q", appWithAdmin.adminSetupToken)
	}
}

func TestParseManagePolicyForm(t *testing.T) {
	tests := []struct {
		name    string
		form    string
		want    string
		wantErr error
	}{
		{
			name: "temporary",
			form: "data_policy=temporary",
			want: "temporary",
		},
		{
			name: "custom duration",
			form: "data_policy=custom&custom_policy=12h",
			want: "12h",
		},
		{
			name: "custom duration trims and lowercases",
			form: "data_policy=custom&custom_policy= 7D ",
			want: "7d",
		},
		{
			name:    "custom duration missing",
			form:    "data_policy=custom",
			wantErr: pastebox.ErrInvalidPolicy,
		},
		{
			name:    "custom duration invalid",
			form:    "data_policy=custom&custom_policy=31d",
			wantErr: pastebox.ErrInvalidPolicy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/paste?manage=token", strings.NewReader(tt.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := req.ParseForm(); err != nil {
				t.Fatalf("ParseForm failed: %v", err)
			}

			got, err := parseManagePolicyForm(req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("parseManagePolicyForm() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("parseManagePolicyForm() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSplitManagePolicy(t *testing.T) {
	tests := []struct {
		policy     string
		wantKind   string
		wantCustom string
	}{
		{policy: "", wantKind: "temporary", wantCustom: ""},
		{policy: "temporary", wantKind: "temporary", wantCustom: ""},
		{policy: "permanent", wantKind: "permanent", wantCustom: ""},
		{policy: "once", wantKind: "once", wantCustom: ""},
		{policy: "12h", wantKind: "custom", wantCustom: "12h"},
	}

	for _, tt := range tests {
		t.Run(tt.policy, func(t *testing.T) {
			gotKind, gotCustom := splitManagePolicy(tt.policy)
			if gotKind != tt.wantKind || gotCustom != tt.wantCustom {
				t.Fatalf("splitManagePolicy(%q) = (%q, %q), want (%q, %q)", tt.policy, gotKind, gotCustom, tt.wantKind, tt.wantCustom)
			}
		})
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
		{name: "makefile by name", filename: "Makefile", want: "text/x-makefile; charset=utf-8"},
		{name: "env example by name", filename: ".env.example", want: "text/x-ini; charset=utf-8"},
		{name: "gitignore by name", filename: ".gitignore", want: "text/x-gitignore; charset=utf-8"},
		{name: "compose yaml by name", filename: "compose.yaml", want: "application/yaml; charset=utf-8"},
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
		{name: "makefile by name", filename: "Makefile", contentType: "text/plain; charset=utf-8", want: "makefile"},
		{name: "env example by name", filename: ".env.example", contentType: "text/plain; charset=utf-8", want: "ini"},
		{name: "gitignore by name", filename: ".gitignore", contentType: "text/plain; charset=utf-8", want: "gitignore"},
		{name: "compose yaml by name", filename: "compose.yaml", contentType: "text/plain; charset=utf-8", want: "yaml"},
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
