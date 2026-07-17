package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testApplication(configPath string, input io.Reader) (application, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return application{
		stdin:      input,
		stdout:     stdout,
		stderr:     stderr,
		httpClient: http.DefaultClient,
		configPath: configPath,
	}, stdout, stderr
}

func serverConfig(t *testing.T, serverURL string) string {
	t.Helper()
	data, err := json.Marshal(map[string]string{"server_url": serverURL})
	if err != nil {
		t.Fatal(err)
	}
	return writeTestConfig(t, string(data))
}

func TestRunUploadStdin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/pastebox/" || r.URL.Query().Get("format") != "json" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "hello from stdin" {
			t.Errorf("body = %q", body)
		}
		if got := r.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Errorf("Content-Type = %q", got)
		}
		if r.Header.Get("data-policy") != "12h" || r.Header.Get("usepassword") != "true" || r.Header.Get("code") != "build-log" || r.Header.Get("label") != "Build log" {
			t.Errorf("unexpected upload headers: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"url":"https://public.example/build-log","expires":"soon","password":"secret","manage":"manage-url","delete":"delete-url"}`)
	}))
	defer server.Close()

	app, stdout, stderr := testApplication(serverConfig(t, server.URL+"/pastebox"), strings.NewReader("hello from stdin"))
	code := app.run([]string{"--expires", "12h", "--password", "--code", "build-log", "--label", "Build log"})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	for _, want := range []string{"url: https://public.example/build-log", "expires: soon", "password: secret", "manage: manage-url", "delete: delete-url"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, missing %q", stdout.String(), want)
		}
	}
}

func TestRunUploadFileMultipart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("Content-Type = %q, err = %v", r.Header.Get("Content-Type"), err)
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
		part, err := reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		if part.FormName() != "file" || part.FileName() != "sample.log" {
			t.Errorf("part = %q %q", part.FormName(), part.FileName())
		}
		body, _ := io.ReadAll(part)
		if string(body) != "file body" {
			t.Errorf("body = %q", body)
		}
		io.WriteString(w, `{"url":"https://public.example/file"}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.log")
	if err := os.WriteFile(filePath, []byte("file body"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, stdout, stderr := testApplication(serverConfig(t, server.URL), strings.NewReader("unused"))
	code := app.run([]string{"--quiet", filePath})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "https://public.example/file\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunUploadArgumentErrors(t *testing.T) {
	app, _, stderr := testApplication("unused", strings.NewReader("body"))
	if code := app.run([]string{"--permanent", "--once"}); code != 2 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunUploadServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":"code already exists"}`)
	}))
	defer server.Close()
	app, _, stderr := testApplication(serverConfig(t, server.URL), strings.NewReader("body"))
	if code := app.run(nil); code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if got := stderr.String(); !strings.Contains(got, "code already exists") || !strings.Contains(got, "HTTP 409") {
		t.Fatalf("stderr = %q", got)
	}
}
