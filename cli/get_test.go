package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pastebox/abc123" || r.URL.Query().Get("raw") != "1" {
			t.Errorf("URL = %s", r.URL.String())
		}
		if r.Header.Get("paste-password") != "top-secret" {
			t.Errorf("paste-password header missing")
		}
		io.WriteString(w, "paste content")
	}))
	defer server.Close()
	app, stdout, stderr := testApplication(serverConfig(t, server.URL+"/pastebox"), strings.NewReader(""))
	if code := app.run([]string{"get", "--password", "top-secret", "abc123"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "paste content" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestResolvePasteURLRejectsDifferentServer(t *testing.T) {
	_, err := resolvePasteURL("https://paste.example.com/base", "https://evil.example/abc")
	if err == nil || !strings.Contains(err.Error(), "configured server") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolvePasteURLRejectsUnsafeTargets(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{target: "https://paste.example.com/base", want: "include a paste code"},
		{target: "https://paste.example.com/other/abc", want: "below configured server path"},
		{target: "https://user:pass@paste.example.com/base/abc", want: "user credentials"},
		{target: "https://paste.example.com/base/abc?delete=secret", want: "query or fragment"},
	}
	for _, tt := range tests {
		_, err := resolvePasteURL("https://paste.example.com/base", tt.target)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("resolvePasteURL(%q) error = %v, want %q", tt.target, err, tt.want)
		}
	}
}

func TestRunConfigValidate(t *testing.T) {
	path := writeTestConfig(t, `{"server_url":"https://paste.example.com/"}`)
	app, stdout, stderr := testApplication(path, bytes.NewReader(nil))
	if code := app.run([]string{"config", "validate"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "valid config: "+path) || !strings.Contains(stdout.String(), "server_url: https://paste.example.com") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
