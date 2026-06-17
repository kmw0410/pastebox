package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"html/template"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	pastebox "pastebox/internal"
)

type app struct {
	store           *pastebox.Store
	index           *template.Template
	paste           *template.Template
	managePage      *template.Template
	adminForm       *template.Template
	adminReset      *template.Template
	adminList       *template.Template
	passwordPage    *template.Template
	notFoundPage    *template.Template
	cloneResult     *template.Template
	i18n            *localizer
	adminSetupToken string
	adminResetToken string
	authLimiter     *authAttemptLimiter
}

type adminActor struct {
	Username string
	ClientIP string
	Remote   string
}

const maxUploadSize int64 = 1 << 30 // 1 GiB
const authFailureWindow = 10 * time.Minute
const authFailureLimit = 20
const uploadSampleSize = 64 * 1024

func newApp(store *pastebox.Store, i18n *localizer, adminResetToken string) (*app, error) {
	adminSetupToken, err := randomBootstrapToken()
	if err != nil {
		return nil, err
	}

	return &app{
		store:           store,
		index:           mustParseTemplate(i18n, "templates/index.html"),
		paste:           mustParseTemplate(i18n, "templates/paste.html"),
		managePage:      mustParseTemplate(i18n, "templates/manage.html"),
		adminForm:       mustParseTemplate(i18n, "templates/admin_form.html"),
		adminReset:      mustParseTemplate(i18n, "templates/admin_reset.html"),
		adminList:       mustParseTemplate(i18n, "templates/admin_list.html"),
		passwordPage:    mustParseTemplate(i18n, "templates/password.html"),
		notFoundPage:    mustParseTemplate(i18n, "templates/404.html"),
		cloneResult:     mustParseTemplate(i18n, "templates/clone.html"),
		i18n:            i18n,
		adminSetupToken: adminSetupToken,
		adminResetToken: adminResetToken,
		authLimiter:     newAuthAttemptLimiter(authFailureWindow, authFailureLimit),
	}, nil
}

func randomBootstrapToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

var errUploadTooLarge = errors.New("upload too large")

func isTextEntry(entry *pastebox.Entry) bool {
	contentType := strings.ToLower(entry.Meta.ContentType)
	if strings.HasPrefix(contentType, "text/") {
		return true
	}

	if strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "yaml") ||
		strings.Contains(contentType, "javascript") ||
		strings.Contains(contentType, "x-sh") {
		return true
	}

	seeker, ok := entry.File.(io.Seeker)
	if !ok {
		return true
	}

	pos, _ := seeker.Seek(0, io.SeekCurrent)

	buf := make([]byte, 4096)
	n, _ := entry.File.Read(buf)
	_, _ = seeker.Seek(pos, io.SeekStart)

	return looksLikeText(buf[:n])
}

func normalizeTextContentType(filename string, contentType string) string {
	switch detected := specialFilenameContentType(filename); detected {
	case "":
	default:
		return detected
	}

	ext := normalizedUploadExt(filename)
	lowerContentType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))

	switch ext {
	case ".log":
		return "text/x-log; charset=utf-8"
	case ".conf", ".cfg", ".ini", ".properties", ".env":
		return "text/x-ini; charset=utf-8"
	case ".rs":
		return "text/x-rust; charset=utf-8"
	case ".go":
		return "text/x-go; charset=utf-8"
	case ".js", ".mjs", ".cjs":
		return "application/javascript; charset=utf-8"
	case ".py":
		return "text/x-python; charset=utf-8"
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".ts", ".tsx":
		return "text/typescript; charset=utf-8"
	case ".php":
		return "application/x-httpd-php; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".csv", ".tsv":
		return "text/csv; charset=utf-8"
	case ".json", ".jsonl":
		return "application/json; charset=utf-8"
	case ".xml":
		return "application/xml; charset=utf-8"
	case ".yaml", ".yml":
		return "application/yaml; charset=utf-8"
	case ".sql":
		return "application/sql; charset=utf-8"
	case ".lua":
		return "text/x-lua; charset=utf-8"
	case ".toml":
		return "application/toml; charset=utf-8"
	case ".sh", ".bash", ".zsh":
		return "text/x-shellscript; charset=utf-8"
	}

	if lowerContentType != "" && isTextContentType(lowerContentType) {
		if strings.Contains(strings.ToLower(contentType), "charset=") {
			return contentType
		}
		return lowerContentType + "; charset=utf-8"
	}

	return "text/plain; charset=utf-8"
}

func specialFilenameContentType(filename string) string {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(filename)))
	switch {
	case base == "dockerfile" || strings.HasSuffix(base, ".dockerfile"):
		return "text/x-dockerfile; charset=utf-8"
	case base == "makefile":
		return "text/x-makefile; charset=utf-8"
	case base == ".env.example":
		return "text/x-ini; charset=utf-8"
	case base == ".gitignore":
		return "text/x-gitignore; charset=utf-8"
	case base == "compose.yaml" || base == "compose.yml" || base == "docker-compose.yaml" || base == "docker-compose.yml":
		return "application/yaml; charset=utf-8"
	case base == "nginx.conf" || strings.HasSuffix(base, ".nginx.conf"):
		return "text/x-nginx-conf; charset=utf-8"
	default:
		return ""
	}
}

func normalizedUploadExt(filename string) string {
	name := strings.ToLower(strings.TrimSpace(filename))
	if name == "" {
		return ""
	}

	if strings.HasSuffix(name, ".tar.gz") {
		return ".tar.gz"
	}
	if strings.HasSuffix(name, ".tar.xz") {
		return ".tar.xz"
	}
	if strings.HasSuffix(name, ".tar.bz2") {
		return ".tar.bz2"
	}

	if strings.HasSuffix(name, ".log") {
		return ".log"
	}

	base := filepath.Base(name)
	baseExt := filepath.Ext(base)
	if len(baseExt) > 1 {
		numericOnly := true
		for _, ch := range baseExt[1:] {
			if ch < '0' || ch > '9' {
				numericOnly = false
				break
			}
		}
		if numericOnly && strings.HasSuffix(strings.TrimSuffix(base, baseExt), ".log") {
			return ".log"
		}
	}

	return filepath.Ext(name)
}

func syntaxLanguage(filename string, contentType string) string {
	switch detected := specialFilenameLanguage(filename); detected {
	case "":
	default:
		return detected
	}

	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))

	switch {
	case strings.Contains(contentType, "x-log"):
		return "logs"
	case strings.Contains(contentType, "yaml"):
		return "yaml"
	case strings.Contains(contentType, "x-makefile"):
		return "makefile"
	case strings.Contains(contentType, "x-ini"):
		return "ini"
	case strings.Contains(contentType, "x-gitignore"):
		return "gitignore"
	case strings.Contains(contentType, "x-rust"):
		return "rust"
	case strings.Contains(contentType, "x-go"):
		return "go"
	case strings.Contains(contentType, "x-shellscript"):
		return "bash"
	case strings.Contains(contentType, "javascript"):
		return "javascript"
	case strings.Contains(contentType, "x-python"):
		return "python"
	case strings.Contains(contentType, "markdown"):
		return "markdown"
	case strings.Contains(contentType, "typescript"):
		return "typescript"
	case strings.Contains(contentType, "sql"):
		return "sql"
	case strings.Contains(contentType, "x-lua"):
		return "lua"
	case strings.Contains(contentType, "toml"):
		return "toml"
	case strings.Contains(contentType, "php"):
		return "php"
	case contentType == "text/html":
		return "xml"
	case contentType == "text/css":
		return "css"
	default:
		return "plaintext"
	}
}

func specialFilenameLanguage(filename string) string {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(filename)))
	switch {
	case base == "dockerfile" || strings.HasSuffix(base, ".dockerfile"):
		return "dockerfile"
	case base == "makefile":
		return "makefile"
	case base == ".env.example":
		return "ini"
	case base == ".gitignore":
		return "gitignore"
	case base == "compose.yaml" || base == "compose.yml" || base == "docker-compose.yaml" || base == "docker-compose.yml":
		return "yaml"
	case base == "nginx.conf" || strings.HasSuffix(base, ".nginx.conf"):
		return "nginx"
	default:
		return ""
	}
}

func responseFormat(r *http.Request) string {
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	switch format {
	case "json", "raw":
		return format
	}

	if r.URL.Query().Get("raw") == "1" {
		return "raw"
	}

	return ""
}

func isBrowserRequest(r *http.Request) bool {
	ua := strings.ToLower(r.UserAgent())
	if strings.HasPrefix(ua, "curl/") || strings.Contains(ua, "wget/") || strings.Contains(ua, "httpie/") {
		return false
	}

	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "text/html") || accept == ""
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	host := r.Host

	if r.TLS != nil {
		scheme = "https"
	}

	if forwarded := r.Header.Get("Forwarded"); forwarded != "" {
		parts := strings.Split(forwarded, ";")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(strings.ToLower(part), "proto=") {
				scheme = strings.Trim(strings.TrimPrefix(part, "proto="), `"`)
			}
			if strings.HasPrefix(strings.ToLower(part), "host=") {
				host = strings.Trim(strings.TrimPrefix(part, "host="), `"`)
			}
		}
	}

	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = strings.Split(proto, ",")[0]
		scheme = strings.TrimSpace(scheme)
	}

	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		host = strings.Split(forwardedHost, ",")[0]
		host = strings.TrimSpace(host)
	}

	if host == "" {
		host = "localhost"
	}

	return scheme + "://" + host
}

func formatExpiresForLog(meta pastebox.Metadata) string {
	if strings.EqualFold(meta.DataPolicy, "permanent") || meta.ExpiresAt.IsZero() {
		return "-"
	}

	return meta.ExpiresAt.Format(time.RFC3339)
}
