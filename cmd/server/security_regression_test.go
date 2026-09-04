package main

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadPastePreviewUTF8(t *testing.T) {
	prefix := strings.Repeat("a", uploadSampleSize+1)
	for _, tc := range []struct {
		name, input, want string
		limit             int64
		truncated         bool
	}{
		{"valid", "hello 한글 😀\n", "hello 한글 😀\n", 100, false},
		{"exact boundary", "한글", "한글", 6, false},
		{"cut two byte rune", "abéz", "ab", 3, true},
		{"cut three byte rune", "ab한z", "ab", 4, true},
		{"cut four byte rune", "ab😀z", "ab", 5, true},
		{"malformed after upload sample", prefix + "\xff\nkept\nend", prefix + "?\nkept\nend", int64(len(prefix) + 30), false},
		{"malformed before byte limit", prefix + "\xff" + strings.Repeat("b", maxInitialHTMLViewBytes), prefix + "?" + strings.Repeat("b", maxInitialHTMLViewBytes-len(prefix)-1), maxInitialHTMLViewBytes, true},
		{"incomplete at EOF", "ok\xe2\x82", "ok??", 100, false},
		{"invalid sequence", "a\xe2(\xa1z", "a?(?z", 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated, remaining, err := readPastePreview(strings.NewReader(tc.input), 400, tc.limit)
			if err != nil || string(got) != tc.want || truncated != tc.truncated || !utf8.Valid(got) || int64(len(got)) > tc.limit {
				t.Fatalf("preview mismatch: length=%d want=%d truncated=%v err=%v", len(got), len(tc.want), truncated, err)
			}
			if !truncated && remaining != 0 {
				t.Fatalf("remaining=%d", remaining)
			}
		})
	}
	got, truncated, remaining, err := readPastePreview(strings.NewReader(prefix+"\xff\nsecond\nthird"), 2, maxInitialHTMLViewBytes)
	if err != nil || string(got) != prefix+"?\nsecond" || !truncated || remaining != 1 {
		t.Fatal("malformed input changed line limit/count")
	}
}

func BenchmarkReadPastePreviewUTF8(b *testing.B) {
	for _, size := range []int{128 << 10, 256 << 10, 512 << 10} {
		for _, malformed := range []bool{false, true} {
			content := bytes.Repeat([]byte("a"), size+1)
			if malformed {
				content[uploadSampleSize+1] = 0xff
			}
			b.Run(fmt.Sprintf("bytes=%d/malformed=%v", size, malformed), func(b *testing.B) {
				b.SetBytes(int64(len(content)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, _, _, err := readPastePreview(bytes.NewReader(content), 400, int64(size)); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestSensitiveResponseHeaders(t *testing.T) {
	t.Chdir("../..")
	a := newTestApp(t)
	a.paste = template.Must(template.New("paste").Parse("{{.Content}}"))
	a.passwordPage = template.Must(template.New("password").Parse("password prompt"))
	a.managePage = template.Must(template.New("manage").Parse("{{.ManageToken}}"))
	a.cloneResult = template.Must(template.New("clone").Parse("{{.ManageURL}}"))
	a.notFoundPage = template.Must(template.New("404").Parse("not found"))
	a.adminForm = template.Must(template.New("admin").Parse("admin form"))
	a.adminReset = template.Must(template.New("reset").Parse("admin reset"))
	a.i18n = &localizer{}
	meta, password, _, token, err := a.store.Create(strings.NewReader("private body"), "test.txt", "text/plain", true, mustParsePolicy(t, "temporary"), "private")
	if err != nil {
		t.Fatal(err)
	}
	path := "/" + meta.ID
	for _, tc := range []struct {
		name, method, path, accept, password, body string
		status                                     int
	}{
		{"authenticated HTML", "GET", path, "text/html", password, "", 200},
		{"authenticated raw", "GET", path + "?raw=1", "text/html", password, "", 200},
		{"authenticated text", "GET", path, "", password, "", 200},
		{"authenticated HEAD", "HEAD", path, "", password, "", 200},
		{"password prompt", "GET", path, "text/html", "", "", 401},
		{"password HEAD", "HEAD", path, "text/html", "", "", 401},
		{"raw unauthorized", "GET", path + "?raw=1", "", "", "", 401},
		{"manage", "GET", path + "?manage=" + token, "text/html", "", "", 200},
		{"manage HEAD", "HEAD", path + "?manage=" + token, "", "", "", 200},
		{"manage error", "GET", path + "?manage=invalid", "text/html", "", "", 404},
		{"upload JSON", "POST", "/?format=json", "", "", "hello", 200},
		{"upload text", "POST", "/", "", "", "hello", 200},
		{"clone HTML", "POST", path + "?clone=1", "text/html", password, "", 200},
		{"clone JSON", "POST", path + "?format=json", "", password, "", 200},
		{"clone password prompt", "POST", path + "?clone=1", "text/html", "", "", 401},
		{"clone JSON error", "POST", path + "?format=json", "", "", "", 401},
		{"upload JSON error", "POST", "/?format=json", "", "", string([]byte{0, 1, 2}), 415},
		{"static prefix redirect", "GET", "/css/../private?password=secret", "", "", "", 307},
		{"method error", "PATCH", path, "", "", "", 405},
		{"missing", "GET", "/missing", "text/html", "", "", 404},
		{"admin redirect", "GET", "/admin", "", "", "", 303},
		{"admin HEAD redirect", "HEAD", "/admin", "", "", "", 303},
		{"admin setup", "GET", "/admin/setup", "text/html", "", "", 200},
		{"admin error", "GET", "/admin/unknown", "", "", "", 404},
		{"canonical redirect", "GET", "/extra/../private?password=secret", "", "", "", 307},
		{"API error", "GET", "/api/v1/pastes/private", "", "", "", 404},
		{"API HEAD", "HEAD", "/api/v1/pastes/private", "", "", "", 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Accept", tc.accept)
			req.Header.Set("paste-password", tc.password)
			if tc.method == "POST" {
				req.Header.Set("usepassword", "true")
			}
			rr := httptest.NewRecorder()
			a.httpHandler().ServeHTTP(rr, req)
			res := rr.Result()
			defer res.Body.Close()
			if res.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d", res.StatusCode, tc.status)
			}
			for key, want := range map[string]string{"Cache-Control": "no-store", "Referrer-Policy": "no-referrer"} {
				if got := res.Header.Get(key); got != want {
					t.Errorf("%s=%q", key, got)
				}
			}
			if strings.HasPrefix(tc.path, "/api/") && (res.Header.Get("X-Frame-Options") != "DENY" || res.Header.Get("X-Content-Type-Options") != "nosniff") {
				t.Fatal("API security headers lost")
			}
			if tc.name == "authenticated raw" && rr.Body.String() != "private body" {
				t.Fatal("raw body changed")
			}
		})
	}
	for _, tc := range []struct{ path, cache string }{{"/css/common.css", "public, max-age=3600"}, {"/js/highlight.min.js", "public, max-age=31536000, immutable"}} {
		rr := httptest.NewRecorder()
		a.httpHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("static status=%d for %s", rr.Code, tc.path)
		}
		if rr.Result().Header.Get("Cache-Control") != tc.cache {
			t.Errorf("static cache changed for %s", tc.path)
		}
	}
}

func TestPreviewMalformedUploadPreservesRaw(t *testing.T) {
	a := newTestApp(t)
	a.paste = template.Must(template.New("paste").Parse("{{.Content}}|{{.Truncated}}|{{.RemainingLines}}"))
	content := strings.Repeat("a", uploadSampleSize+1) + "\xff" + strings.Repeat("b", maxInitialHTMLViewBytes) + "\nlast"
	req := httptest.NewRequest("POST", "/", strings.NewReader(content))
	req.Header.Set("code", "badutf8")
	rr := httptest.NewRecorder()
	handler := a.httpHandler()
	handler.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("upload status=%d", rr.Code)
	}
	req = httptest.NewRequest("GET", "/badutf8", nil)
	req.Header.Set("Accept", "text/html")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	want := strings.Repeat("a", uploadSampleSize+1) + "?" + strings.Repeat("b", maxInitialHTMLViewBytes-uploadSampleSize-2) + "|true|1"
	if rr.Code != 200 || rr.Body.String() != want {
		t.Fatal("uploaded malformed content preview mismatch")
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/badutf8?raw=1", nil))
	if rr.Code != 200 || rr.Body.String() != content {
		t.Fatal("raw content changed")
	}
}
