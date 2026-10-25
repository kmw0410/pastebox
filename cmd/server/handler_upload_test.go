package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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

func TestUploadHandlerRawBodyPreservesFilenameForSyntaxHighlighting(t *testing.T) {
	tests := []struct {
		name               string
		header             string
		contentDisposition string
		wantFilename       string
		wantContentType    string
		wantLanguage       string
	}{
		{
			name:            "stdin without filename",
			wantFilename:    "",
			wantContentType: "text/plain; charset=utf-8",
			wantLanguage:    "plaintext",
		},
		{
			name:            "filename header",
			header:          `scripts/deploy.sh`,
			wantFilename:    "deploy.sh",
			wantContentType: "text/x-shellscript; charset=utf-8",
			wantLanguage:    "bash",
		},
		{
			name:               "content disposition",
			contentDisposition: `attachment; filename="service.go"`,
			wantFilename:       "service.go",
			wantContentType:    "text/x-go; charset=utf-8",
			wantLanguage:       "go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestApp(t)
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("package main\n"))
			req.Header.Set("Content-Type", "text/plain")
			if tt.header != "" {
				req.Header.Set("filename", tt.header)
			}
			if tt.contentDisposition != "" {
				req.Header.Set("Content-Disposition", tt.contentDisposition)
			}
			rr := httptest.NewRecorder()

			app.uploadHandler(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%q", rr.Code, rr.Body.String())
			}

			id := strings.TrimPrefix(responseLineValue(rr.Body.String(), "url"), "http://example.com/")
			entry, err := app.store.Open(id, "")
			if err != nil {
				t.Fatalf("Open failed: %v", err)
			}
			defer entry.File.Close()
			if entry.Meta.Filename != tt.wantFilename {
				t.Fatalf("filename = %q, want %q", entry.Meta.Filename, tt.wantFilename)
			}
			if entry.Meta.ContentType != tt.wantContentType {
				t.Fatalf("content type = %q, want %q", entry.Meta.ContentType, tt.wantContentType)
			}
			if got := syntaxLanguage(entry.Meta.Filename, entry.Meta.ContentType); got != tt.wantLanguage {
				t.Fatalf("syntax language = %q, want %q", got, tt.wantLanguage)
			}
		})
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
	req.Header.Set("password", "custom-secret")
	rr := httptest.NewRecorder()

	app.uploadHandler(rr, req)

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
	entry, err := app.store.Open("prompted", "custom-secret")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	_ = entry.File.Close()
}

func TestUploadResponseOmitsDeleteLink(t *testing.T) {
	app := newTestApp(t)

	plainRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("plain response"))
	plainRequest.Header.Set("Content-Type", "text/plain")
	plainRequest.Header.Set("code", "nodelete1")
	plainResponse := httptest.NewRecorder()
	app.uploadHandler(plainResponse, plainRequest)
	if plainResponse.Code != http.StatusOK {
		t.Fatalf("plain status = %d, body=%q", plainResponse.Code, plainResponse.Body.String())
	}
	if strings.Contains(plainResponse.Body.String(), "delete:") || strings.Contains(plainResponse.Body.String(), "?delete=") {
		t.Fatalf("plain response exposed delete link: %q", plainResponse.Body.String())
	}
	if !strings.Contains(plainResponse.Body.String(), "manage:") {
		t.Fatalf("plain response omitted manage link: %q", plainResponse.Body.String())
	}

	jsonRequest := httptest.NewRequest(http.MethodPost, "/?format=json", strings.NewReader("json response"))
	jsonRequest.Header.Set("Content-Type", "text/plain")
	jsonRequest.Header.Set("code", "nodelete2")
	jsonResponse := httptest.NewRecorder()
	app.uploadHandler(jsonResponse, jsonRequest)
	if jsonResponse.Code != http.StatusOK {
		t.Fatalf("JSON status = %d, body=%q", jsonResponse.Code, jsonResponse.Body.String())
	}
	var fields map[string]any
	if err := json.Unmarshal(jsonResponse.Body.Bytes(), &fields); err != nil {
		t.Fatalf("decode JSON response: %v", err)
	}
	if _, ok := fields["delete"]; ok {
		t.Fatalf("JSON response exposed delete link: %q", jsonResponse.Body.String())
	}
	if _, ok := fields["manage"]; !ok {
		t.Fatalf("JSON response omitted manage link: %q", jsonResponse.Body.String())
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
		{name: "kdl by ext", filename: "config.kdl", want: "text/x-kdl; charset=utf-8"},
		{name: "rotated numeric log", filename: "access.log.1", want: "text/x-log; charset=utf-8"},
		{name: "rotated named log", filename: "access.log.old", want: "text/x-log; charset=utf-8"},
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
