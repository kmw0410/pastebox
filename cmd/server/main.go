package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

func main() {
	listenAddr := getenv("LISTEN_ADDR", ":8080")
	dataDir := getenv("DATA_DIR", "/paste-data")
	expireDays := getenvInt("EXPIRE_DAYS", 30)
	i18n := loadLocalizer(getenv("LANGUAGE", "en"))
	adminSetupToken, err := randomBootstrapToken()
	if err != nil {
		log.Fatalf("failed to generate admin setup token: %v", err)
	}
	adminResetToken := strings.TrimSpace(os.Getenv("ADMIN_RESET_TOKEN"))

	store, err := pastebox.NewStore(dataDir, time.Duration(expireDays)*24*time.Hour)
	if err != nil {
		log.Fatalf("failed to initialize store: %v", err)
	}

	a := &app{
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
	}

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			if err := store.CleanupExpired(); err != nil {
				log.Printf("cleanup failed: %v", err)
			}
			<-ticker.C
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handle)

	log.Printf("pastebox listening on %s, data=%s", listenAddr, dataDir)
	log.Printf("admin setup token: %s", adminSetupToken)

	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatal(err)
	}
}

func randomBootstrapToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (a *app) handle(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/admin") {
		a.adminHandler(w, r)
		return
	}

	if r.URL.Path == "/" {
		switch r.Method {
		case http.MethodGet:
			a.indexHandler(w, r)
		case http.MethodPost, http.MethodPut:
			a.uploadHandler(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/")
	if strings.Contains(id, "/") || id == "" {
		a.notFoundHandler(w, r)
		return
	}

	if r.Method == http.MethodPost {
		if r.URL.Query().Get("manage") != "" {
			a.manageHandler(w, r, id)
			return
		}
		a.cloneHandler(w, r, id)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if token := r.URL.Query().Get("delete"); token != "" {
		a.deleteHandler(w, r, id, token)
		return
	}

	if token := r.URL.Query().Get("manage"); token != "" {
		a.manageHandler(w, r, id)
		return
	}

	a.viewHandler(w, r, id)
}

func (a *app) indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	data := map[string]string{
		"BaseURL": requestBaseURL(r),
	}

	if err := a.index.Execute(w, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (a *app) uploadHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	disabled, err := a.store.UploadsDisabled()
	if err != nil {
		log.Printf("failed to read upload status: %v", err)
		http.Error(w, "upload status unavailable", http.StatusInternalServerError)
		return
	}

	if disabled {
		a.respondRequestError(w, r, http.StatusServiceUnavailable, "new uploads are currently disabled")
		return
	}

	var reader io.Reader
	var filename string
	contentType := r.Header.Get("Content-Type")

	if strings.HasPrefix(strings.ToLower(contentType), "multipart/form-data") {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "request body too large") {
				a.respondRequestError(w, r, http.StatusRequestEntityTooLarge, "upload too large. maximum size is 1GB")
				return
			}

			a.respondRequestError(w, r, http.StatusBadRequest, "invalid multipart form")
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			a.respondRequestError(w, r, http.StatusBadRequest, "missing file field")
			return
		}
		defer file.Close()

		reader = file

		if header != nil {
			filename = header.Filename

			if header.Size > maxUploadSize {
				a.respondRequestError(w, r, http.StatusRequestEntityTooLarge, "upload too large. maximum size is 1GB")
				return
			}

			if partType := strings.TrimSpace(header.Header.Get("Content-Type")); partType != "" {
				contentType = partType
			}
			if detected := mime.TypeByExtension(strings.ToLower(filepath.Ext(header.Filename))); detected != "" {
				contentType = detected
			}
		}
	} else {
		reader = r.Body
		if strings.TrimSpace(contentType) == "" {
			contentType = "text/plain; charset=utf-8"
		}
	}

	tempFile, sample, err := spoolUploadToTemp(reader, maxUploadSize, uploadSampleSize)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			a.respondRequestError(w, r, http.StatusRequestEntityTooLarge, "upload too large. maximum size is 1GB")
			return
		}
		a.respondRequestError(w, r, http.StatusBadRequest, "failed to read upload")
		return
	}
	defer func() {
		_ = os.Remove(tempFile.Name())
		_ = tempFile.Close()
	}()

	allowed, reason := allowTextUpload(filename, contentType, sample)
	if !allowed {
		log.Printf("upload blocked: remote=%s filename=%q content_type=%q reason=%s", r.RemoteAddr, filename, contentType, reason)
		a.respondRequestError(w, r, http.StatusUnsupportedMediaType, "unsupported file type. only text-based files are allowed")
		return
	}

	contentType = normalizeTextContentType(filename, contentType)

	usePassword := strings.EqualFold(strings.TrimSpace(r.Header.Get("usepassword")), "true")
	policy := strings.ToLower(strings.TrimSpace(r.Header.Get("data-policy")))
	permanent := policy == "permanent"
	once := policy == "once"
	customCode := strings.TrimSpace(r.Header.Get("code"))

	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		a.respondRequestError(w, r, http.StatusInternalServerError, "failed to process upload")
		return
	}

	meta, password, deleteToken, manageToken, err := a.store.Create(tempFile, filename, contentType, usePassword, permanent, once, customCode)
	if err != nil {
		log.Printf("upload failed: %v", err)

		if errors.Is(err, pastebox.ErrInvalidCode) {
			a.respondRequestError(w, r, http.StatusBadRequest, "invalid code. use 1-10 characters: letters, numbers, underscore, or hyphen")
			return
		}

		if errors.Is(err, pastebox.ErrCodeExists) {
			a.respondRequestError(w, r, http.StatusConflict, "code already exists")
			return
		}

		a.respondRequestError(w, r, http.StatusInternalServerError, "upload failed")
		return
	}

	log.Printf(
		"created: id=%s remote=%s size=%d content_type=%q policy=%s expires=%s protected=%t",
		meta.ID,
		r.RemoteAddr,
		meta.Size,
		meta.ContentType,
		meta.DataPolicy,
		formatExpiresForLog(meta),
		password != "",
	)

	a.writeUploadResponse(w, r, meta, password, deleteToken, manageToken)
}

func (a *app) writeUploadResponse(w http.ResponseWriter, r *http.Request, meta pastebox.Metadata, password string, deleteToken string, manageToken string) {
	a.writeUploadResponseWithMode(w, r, meta, password, deleteToken, manageToken, false)
}

func (a *app) writeCloneResponse(w http.ResponseWriter, r *http.Request, meta pastebox.Metadata, password string, deleteToken string, manageToken string) {
	a.writeUploadResponseWithMode(w, r, meta, password, deleteToken, manageToken, true)
}

func (a *app) writeUploadResponseWithMode(w http.ResponseWriter, r *http.Request, meta pastebox.Metadata, password string, deleteToken string, manageToken string, clone bool) {
	url := strings.TrimRight(requestBaseURL(r), "/") + "/" + meta.ID
	deleteURL := url + "?delete=" + deleteToken
	manageURL := url + "?manage=" + manageToken
	format := responseFormat(r)

	expires := ""
	if !strings.EqualFold(meta.DataPolicy, "permanent") && !meta.ExpiresAt.IsZero() {
		expires = meta.ExpiresAt.Format(time.RFC3339)
	}

	if format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		resp := uploadResponse{
			URL:      url,
			Expires:  expires,
			Password: password,
			Manage:   manageURL,
			Delete:   deleteURL,
		}
		_ = writePrettyJSON(w, resp)
		return
	}

	if clone && isBrowserRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		_ = a.cloneResult.Execute(w, map[string]any{
			"URL":       url,
			"Expires":   expires,
			"Password":  password,
			"DeleteURL": deleteURL,
			"ManageURL": manageURL,
		})
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "url: %s\n", url)

	if expires != "" {
		fmt.Fprintf(w, "expires: %s\n", expires)
	}

	if password != "" {
		fmt.Fprintf(w, "password: %s\n", password)
	}

	fmt.Fprintf(w, "manage: %s\n", manageURL)
	fmt.Fprintf(w, "delete: %s\n", deleteURL)
}

type uploadResponse struct {
	URL      string `json:"url"`
	Expires  string `json:"expires,omitempty"`
	Password string `json:"password,omitempty"`
	Manage   string `json:"manage,omitempty"`
	Delete   string `json:"delete,omitempty"`
}

func (a *app) cloneHandler(w http.ResponseWriter, r *http.Request, id string) {
	disabled, err := a.store.UploadsDisabled()
	if err != nil {
		log.Printf("failed to read upload status: %v", err)
		http.Error(w, "upload status unavailable", http.StatusInternalServerError)
		return
	}

	if disabled {
		a.respondRequestError(w, r, http.StatusServiceUnavailable, "new uploads are currently disabled")
		return
	}

	password := r.FormValue("password")
	if password == "" {
		password = r.URL.Query().Get("password")
	}
	if password == "" {
		password = r.Header.Get("paste-password")
	}

	usePassword := strings.EqualFold(strings.TrimSpace(r.Header.Get("usepassword")), "true")
	policy := strings.ToLower(strings.TrimSpace(r.Header.Get("data-policy")))
	permanent := policy == "permanent"
	once := policy == "once"
	customCode := strings.TrimSpace(r.Header.Get("code"))

	meta, newPassword, deleteToken, manageToken, err := a.store.Clone(id, password, usePassword, permanent, once, customCode)
	if err != nil {
		if errors.Is(err, pastebox.ErrInvalidPassword) {
			a.respondRequestError(w, r, http.StatusUnauthorized, "password required or invalid. use ?password=... or paste-password header")
			return
		}

		if errors.Is(err, pastebox.ErrInvalidCode) {
			a.respondRequestError(w, r, http.StatusBadRequest, "invalid code. use 1-10 characters: letters, numbers, underscore, or hyphen")
			return
		}

		if errors.Is(err, pastebox.ErrCodeExists) {
			a.respondRequestError(w, r, http.StatusConflict, "code already exists")
			return
		}

		http.NotFound(w, r)
		return
	}

	log.Printf(
		"cloned: source=%s id=%s remote=%s size=%d content_type=%q policy=%s expires=%s protected=%t",
		id,
		meta.ID,
		r.RemoteAddr,
		meta.Size,
		meta.ContentType,
		meta.DataPolicy,
		formatExpiresForLog(meta),
		newPassword != "",
	)

	a.writeCloneResponse(w, r, meta, newPassword, deleteToken, manageToken)
}

func (a *app) deleteHandler(w http.ResponseWriter, r *http.Request, id string, token string) {
	if r.Method == http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := a.store.Delete(id, token); err != nil {
		if errors.Is(err, pastebox.ErrInvalidDeleteToken) {
			log.Printf("delete denied: id=%s remote=%s", id, r.RemoteAddr)
			http.Error(w, "delete token required or invalid", http.StatusUnauthorized)
			return
		}

		http.NotFound(w, r)
		return
	}

	log.Printf("deleted: id=%s remote=%s", id, r.RemoteAddr)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "deleted")
}

type managePageData struct {
	ID                string
	Filename          string
	PublicURL         string
	ManageURL         string
	CreatedAt         time.Time
	ExpiresAt         time.Time
	DataPolicy        string
	PasswordProtected bool
	Notice            string
	Error             string
	GeneratedPassword string
	Deleted           bool
}

func (a *app) manageHandler(w http.ResponseWriter, r *http.Request, id string) {
	token := strings.TrimSpace(r.URL.Query().Get("manage"))
	if token == "" {
		a.notFoundHandler(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		meta, err := a.store.ManageMetadata(id, token)
		if err != nil {
			a.notFoundHandler(w, r)
			return
		}

		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}

		a.renderManagePage(w, r, meta, token, "", "", "", false)
		return
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			a.renderManageError(w, r, id, token, a.i18n.T("manage_error_invalid_form"))
			return
		}

		action := strings.ToLower(strings.TrimSpace(r.FormValue("manage_action")))
		switch action {
		case "enable_password":
			meta, password, err := a.store.SetPasswordProtection(id, token)
			if err != nil {
				a.renderManageError(w, r, id, token, a.manageErrorMessage(err))
				return
			}
			a.renderManagePage(w, r, meta, token, a.i18n.T("manage_password_enabled"), "", password, false)
		case "disable_password":
			password := r.FormValue("password")
			meta, err := a.store.ClearPasswordProtection(id, token, password)
			if err != nil {
				a.renderManageError(w, r, id, token, a.manageErrorMessage(err))
				return
			}
			a.renderManagePage(w, r, meta, token, a.i18n.T("manage_password_disabled"), "", "", false)
		case "set_policy":
			policy := strings.ToLower(strings.TrimSpace(r.FormValue("data_policy")))
			meta, err := a.store.SetDataPolicy(id, token, policy)
			if err != nil {
				a.renderManageError(w, r, id, token, a.manageErrorMessage(err))
				return
			}
			a.renderManagePage(w, r, meta, token, a.i18n.T("manage_policy_updated"), "", "", false)
		case "delete":
			if err := a.store.DeleteManaged(id, token); err != nil {
				a.renderManageError(w, r, id, token, a.manageErrorMessage(err))
				return
			}
			a.renderManageDeleted(w, r, id, token)
		default:
			a.renderManageError(w, r, id, token, a.i18n.T("manage_error_invalid_action"))
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *app) renderManageDeleted(w http.ResponseWriter, r *http.Request, id string, token string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	baseURL := requestBaseURL(r)
	publicURL := strings.TrimRight(baseURL, "/") + "/" + id

	_ = a.managePage.Execute(w, managePageData{
		ID:        id,
		PublicURL: publicURL,
		ManageURL: publicURL + "?manage=" + token,
		Notice:    a.i18n.T("manage_deleted"),
		Deleted:   true,
	})
}

func (a *app) renderManageError(w http.ResponseWriter, r *http.Request, id string, token string, message string) {
	meta, err := a.store.ManageMetadata(id, token)
	if err != nil {
		a.notFoundHandler(w, r)
		return
	}
	a.renderManagePage(w, r, meta, token, "", message, "", false)
}

func (a *app) renderManagePage(w http.ResponseWriter, r *http.Request, meta pastebox.Metadata, token string, notice string, errMsg string, generatedPassword string, deleted bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	baseURL := requestBaseURL(r)
	publicURL := strings.TrimRight(baseURL, "/") + "/" + meta.ID
	manageURL := publicURL + "?manage=" + token

	_ = a.managePage.Execute(w, managePageData{
		ID:                meta.ID,
		Filename:          meta.Filename,
		PublicURL:         publicURL,
		ManageURL:         manageURL,
		CreatedAt:         meta.CreatedAt,
		ExpiresAt:         meta.ExpiresAt,
		DataPolicy:        meta.DataPolicy,
		PasswordProtected: meta.PasswordHash != "",
		Notice:            notice,
		Error:             errMsg,
		GeneratedPassword: generatedPassword,
		Deleted:           deleted,
	})
}

func (a *app) manageErrorMessage(err error) string {
	switch {
	case errors.Is(err, pastebox.ErrInvalidPassword):
		return a.i18n.T("manage_error_invalid_password")
	case errors.Is(err, pastebox.ErrInvalidManageToken):
		return a.i18n.T("manage_error_invalid_token")
	case errors.Is(err, pastebox.ErrAlreadyProtected):
		return a.i18n.T("manage_error_already_protected")
	case errors.Is(err, pastebox.ErrInvalidPolicy):
		return a.i18n.T("manage_error_invalid_policy")
	default:
		return a.i18n.T("manage_error_generic")
	}
}

func (a *app) viewHandler(w http.ResponseWriter, r *http.Request, id string) {
	password := r.URL.Query().Get("password")
	if password == "" {
		password = r.Header.Get("paste-password")
	}

	if r.Method == http.MethodHead {
		entry, err := a.store.Open(id, password)
		if err != nil {
			if errors.Is(err, pastebox.ErrInvalidPassword) {
				a.passwordRequiredHandler(w, r, id)
				return
			}
			a.notFoundHandler(w, r)
			return
		}
		defer entry.File.Close()

		a.writeViewHeaders(w, entry)
		return
	}

	err := a.store.View(id, password, func(entry *pastebox.Entry) error {
		raw := responseFormat(r) == "raw"
		browser := isBrowserRequest(r)

		if !raw && browser && isTextEntry(entry) {
			content, err := io.ReadAll(entry.File)
			if err != nil {
				return err
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)

			return a.paste.Execute(w, map[string]any{
				"ID":       entry.Meta.ID,
				"Filename": entry.Meta.Filename,
				"Content":  string(content),
				"Language": syntaxLanguage(entry.Meta.ContentType),
				"Password": password,
			})
		}

		a.writeViewHeaders(w, entry)
		_, err := io.Copy(w, entry.File)
		return err
	})
	if err != nil {
		if errors.Is(err, pastebox.ErrInvalidPassword) {
			a.passwordRequiredHandler(w, r, id)
			return
		}
		a.notFoundHandler(w, r)
		return
	}
}

func (a *app) writeViewHeaders(w http.ResponseWriter, entry *pastebox.Entry) {
	contentType := entry.Meta.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if isTextEntry(entry) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, entry.Meta.ID))
}

func (a *app) passwordRequiredHandler(w http.ResponseWriter, r *http.Request, id string) {
	if !isBrowserRequest(r) {
		http.Error(w, "password required or invalid. use ?password=... or paste-password header", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)

	_ = a.passwordPage.Execute(w, map[string]any{
		"ID":     id,
		"Action": "/" + id,
	})
}

func (a *app) notFoundHandler(w http.ResponseWriter, r *http.Request) {
	if !isBrowserRequest(r) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)

	_ = a.notFoundPage.Execute(w, map[string]any{
		"Path": r.URL.Path,
	})
}

func (a *app) adminHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/admin", "/admin/":
		a.adminIndexHandler(w, r)
	case "/admin/setup":
		a.adminSetupHandler(w, r)
	case "/admin/login":
		a.adminLoginHandler(w, r)
	case "/admin/reset":
		a.adminResetHandler(w, r)
	case "/admin/logout":
		a.adminLogoutHandler(w, r)
	case "/admin/delete":
		a.adminDeleteHandler(w, r)
	case "/admin/delete-all":
		a.adminDeleteAllHandler(w, r)
	case "/admin/uploads":
		a.adminUploadsHandler(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (a *app) adminIndexHandler(w http.ResponseWriter, r *http.Request) {
	exists, err := a.store.AdminExists()
	if err != nil {
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return
	}

	if !exists {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}

	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}

	items, err := a.store.ListPastes()
	if err != nil {
		http.Error(w, "failed to list pastes", http.StatusInternalServerError)
		return
	}
	items = localizeAdminPasteItems(items, time.Local)

	uploadsDisabled, err := a.store.UploadsDisabled()
	if err != nil {
		http.Error(w, "failed to read upload status", http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"Items":           items,
		"Stats":           buildAdminStats(items),
		"BaseURL":         requestBaseURL(r),
		"UploadsDisabled": uploadsDisabled,
		"Notice":          popAdminFlash(w, r),
		"CSRFToken":       issueAdminCSRFToken(w, r),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.adminList.Execute(w, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

const adminFlashCookieName = "pastebox_admin_flash"

func setAdminFlash(w http.ResponseWriter, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminFlashCookieName,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(message)),
		Path:     "/admin",
		MaxAge:   60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func popAdminFlash(w http.ResponseWriter, r *http.Request) string {
	cookie, err := r.Cookie(adminFlashCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return ""
	}

	http.SetCookie(w, &http.Cookie{
		Name:     adminFlashCookieName,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	message, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return ""
	}

	return string(message)
}

type adminStats struct {
	Total          int
	Temporary      int
	Permanent      int
	Protected      int
	Expiring24h    int
	Expired        int
	TotalSizeBytes int64
	TotalSize      string
}

func buildAdminStats(items []pastebox.AdminPasteItem) adminStats {
	now := time.Now().UTC()
	stats := adminStats{
		Total: len(items),
	}

	for _, item := range items {
		stats.TotalSizeBytes += item.Size

		if strings.EqualFold(item.DataPolicy, "permanent") {
			stats.Permanent++
		} else {
			stats.Temporary++
		}

		if item.Protected {
			stats.Protected++
		}

		if !item.ExpiresAt.IsZero() {
			if now.After(item.ExpiresAt) {
				stats.Expired++
			} else if item.ExpiresAt.Sub(now) <= 24*time.Hour {
				stats.Expiring24h++
			}
		}
	}

	stats.TotalSize = formatBytes(stats.TotalSizeBytes)
	return stats
}

func localizeAdminPasteItems(items []pastebox.AdminPasteItem, loc *time.Location) []pastebox.AdminPasteItem {
	if loc == nil {
		return items
	}

	out := make([]pastebox.AdminPasteItem, len(items))
	copy(out, items)

	for i := range out {
		out[i].CreatedAt = out[i].CreatedAt.In(loc)
		if !out[i].ExpiresAt.IsZero() {
			out[i].ExpiresAt = out[i].ExpiresAt.In(loc)
		}
	}

	return out
}

func formatBytes(size int64) string {
	const unit = 1024

	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div := int64(unit)
	exp := 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

func (a *app) adminSetupHandler(w http.ResponseWriter, r *http.Request) {
	exists, err := a.store.AdminExists()
	if err != nil {
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return
	}

	if exists {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodGet {
		a.renderAdminForm(w, a.i18n.T("admin_setup_title"), "/admin/setup", "", a.i18n.T("admin_create_button"))
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !verifyAdminCSRFToken(w, r) {
		log.Printf("csrf validation failed: path=%s remote=%s", r.URL.Path, r.RemoteAddr)
		a.renderAdminForm(w, a.i18n.T("admin_setup_title"), "/admin/setup", a.i18n.T("admin_error_invalid_form"), a.i18n.T("admin_create_button"))
		return
	}

	if err := r.ParseForm(); err != nil {
		a.renderAdminForm(w, a.i18n.T("admin_setup_title"), "/admin/setup", a.i18n.T("admin_error_invalid_form"), a.i18n.T("admin_create_button"))
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	setupToken := strings.TrimSpace(r.FormValue("setup_token"))

	if subtle.ConstantTimeCompare([]byte(setupToken), []byte(a.adminSetupToken)) != 1 {
		a.renderAdminForm(w, a.i18n.T("admin_setup_title"), "/admin/setup", a.i18n.T("admin_error_invalid_setup_token"), a.i18n.T("admin_create_button"))
		return
	}

	if err := a.store.CreateAdmin(username, password); err != nil {
		a.renderAdminForm(w, a.i18n.T("admin_setup_title"), "/admin/setup", err.Error(), a.i18n.T("admin_create_button"))
		return
	}

	log.Printf("admin created: username=%s remote=%s", username, r.RemoteAddr)

	token, err := a.store.CreateAdminSession()
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	setAdminCookie(w, token)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *app) adminLoginHandler(w http.ResponseWriter, r *http.Request) {
	exists, err := a.store.AdminExists()
	if err != nil {
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return
	}

	if !exists {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}

	if r.Method == http.MethodGet {
		a.renderAdminForm(w, a.i18n.T("admin_login_title"), "/admin/login", "", a.i18n.T("admin_login_button"))
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !verifyAdminCSRFToken(w, r) {
		log.Printf("csrf validation failed: path=%s remote=%s", r.URL.Path, r.RemoteAddr)
		a.renderAdminForm(w, a.i18n.T("admin_login_title"), "/admin/login", a.i18n.T("admin_error_invalid_form"), a.i18n.T("admin_login_button"))
		return
	}

	if err := r.ParseForm(); err != nil {
		a.renderAdminForm(w, a.i18n.T("admin_login_title"), "/admin/login", a.i18n.T("admin_error_invalid_form"), a.i18n.T("admin_login_button"))
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	clientIP := requestClientIP(r)
	usernameKey := "admin_login:user:" + strings.ToLower(strings.TrimSpace(username))
	ipKey := "admin_login:ip:" + clientIP
	pairKey := "admin_login:pair:" + clientIP + ":" + strings.ToLower(strings.TrimSpace(username))
	if retryAfter, blocked := a.authLimiter.retryAfterAny([]string{ipKey, usernameKey, pairKey}, time.Now()); blocked {
		log.Printf("admin login rate limited: ip=%s remote=%s retry_after=%s", clientIP, r.RemoteAddr, retryAfter.Round(time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		http.Error(w, "too many failed attempts. try again later", http.StatusTooManyRequests)
		return
	}

	ok, err := a.store.AuthenticateAdmin(username, password)
	if err != nil {
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return
	}

	if !ok {
		a.authLimiter.recordFailureMany([]string{ipKey, usernameKey, pairKey}, time.Now())
		log.Printf("admin login failed: username=%s remote=%s", username, r.RemoteAddr)
		a.renderAdminForm(w, a.i18n.T("admin_login_title"), "/admin/login", a.i18n.T("admin_error_invalid_credentials"), a.i18n.T("admin_login_button"))
		return
	}
	a.authLimiter.clearMany([]string{ipKey, usernameKey, pairKey})

	token, err := a.store.CreateAdminSession()
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	log.Printf("admin login: username=%s remote=%s", username, r.RemoteAddr)

	setAdminCookie(w, token)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *app) adminLogoutHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("pastebox_admin")
	if err == nil {
		_ = a.store.DeleteAdminSession(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "pastebox_admin",
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (a *app) adminResetHandler(w http.ResponseWriter, r *http.Request) {
	exists, err := a.store.AdminExists()
	if err != nil {
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Redirect(w, r, "/admin/setup", http.StatusSeeOther)
		return
	}

	if strings.TrimSpace(a.adminResetToken) == "" {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodGet {
		a.renderAdminResetForm(w, "")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !verifyAdminCSRFToken(w, r) {
		log.Printf("csrf validation failed: path=%s remote=%s", r.URL.Path, r.RemoteAddr)
		a.renderAdminResetForm(w, a.i18n.T("admin_error_invalid_form"))
		return
	}

	if err := r.ParseForm(); err != nil {
		a.renderAdminResetForm(w, a.i18n.T("admin_error_invalid_form"))
		return
	}

	resetToken := strings.TrimSpace(r.FormValue("reset_token"))
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")
	clientIP := requestClientIP(r)
	limitKey := "admin_reset:" + clientIP
	if retryAfter, blocked := a.authLimiter.retryAfter(limitKey, time.Now()); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
		http.Error(w, "too many failed attempts. try again later", http.StatusTooManyRequests)
		return
	}

	if subtle.ConstantTimeCompare([]byte(resetToken), []byte(a.adminResetToken)) != 1 {
		a.authLimiter.recordFailure(limitKey, time.Now())
		a.renderAdminResetForm(w, a.i18n.T("admin_reset_error_invalid_token"))
		return
	}

	if strings.TrimSpace(password) == "" {
		a.renderAdminResetForm(w, a.i18n.T("admin_reset_error_password_required"))
		return
	}
	if password != passwordConfirm {
		a.renderAdminResetForm(w, a.i18n.T("admin_reset_error_password_mismatch"))
		return
	}

	if err := a.store.ForceResetAdminPassword(password); err != nil {
		http.Error(w, "failed to reset password", http.StatusInternalServerError)
		return
	}
	a.authLimiter.clear(limitKey)

	log.Printf("admin password reset: remote=%s", r.RemoteAddr)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (a *app) adminUploadsHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !verifyAdminCSRFToken(w, r) {
		log.Printf("csrf validation failed: path=%s remote=%s", r.URL.Path, r.RemoteAddr)
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	disabled := r.FormValue("disabled") == "true"

	if err := a.store.SetUploadsDisabled(disabled); err != nil {
		http.Error(w, "failed to update upload status", http.StatusInternalServerError)
		return
	}

	log.Printf("admin upload status changed: disabled=%t remote=%s", disabled, r.RemoteAddr)

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *app) adminDeleteAllHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !verifyAdminCSRFToken(w, r) {
		log.Printf("csrf validation failed: path=%s remote=%s", r.URL.Path, r.RemoteAddr)
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	count, err := a.store.AdminDeleteAll()
	if err != nil {
		log.Printf("admin delete all failed: remote=%s deleted=%d err=%v", r.RemoteAddr, count, err)
		http.Error(w, "delete all failed", http.StatusInternalServerError)
		return
	}

	log.Printf("admin deleted all pastes: count=%d remote=%s", count, r.RemoteAddr)

	setAdminFlash(w, fmt.Sprintf(a.i18n.T("admin_flash_delete_all"), count))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *app) adminDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !verifyAdminCSRFToken(w, r) {
		log.Printf("csrf validation failed: path=%s remote=%s", r.URL.Path, r.RemoteAddr)
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	ids := r.Form["ids"]
	if len(ids) > 0 {
		deleted := 0
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if err := a.store.AdminDelete(id); err != nil {
				http.Error(w, "delete failed", http.StatusBadRequest)
				return
			}
			deleted++
		}
		if deleted > 0 {
			log.Printf("admin deleted selected pastes: count=%d remote=%s", deleted, r.RemoteAddr)
			setAdminFlash(w, fmt.Sprintf(a.i18n.T("admin_flash_delete_all"), deleted))
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	id := r.FormValue("id")
	if err := a.store.AdminDelete(id); err != nil {
		http.Error(w, "delete failed", http.StatusBadRequest)
		return
	}

	log.Printf("admin deleted: id=%s remote=%s", id, r.RemoteAddr)

	setAdminFlash(w, fmt.Sprintf(a.i18n.T("admin_flash_delete_one"), id))
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *app) requireAdmin(w http.ResponseWriter, r *http.Request) (adminActor, bool) {
	actor := adminActor{
		ClientIP: requestClientIP(r),
		Remote:   r.RemoteAddr,
	}

	cookie, err := r.Cookie("pastebox_admin")
	if err != nil {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return actor, false
	}

	ok, err := a.store.ValidAdminSession(cookie.Value)
	if err != nil || !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return actor, false
	}

	username, err := a.store.AdminUsername()
	if err != nil {
		http.Error(w, "admin database error", http.StatusInternalServerError)
		return actor, false
	}

	actor.Username = username
	return actor, true
}

func (a *app) renderAdminForm(w http.ResponseWriter, title string, action string, errorMessage string, button string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	description := a.i18n.T("admin_login_description")
	if action == "/admin/setup" {
		description = a.i18n.T("admin_setup_description")
	}

	_ = a.adminForm.Execute(w, map[string]any{
		"Title":             title,
		"Action":            action,
		"Error":             errorMessage,
		"Button":            button,
		"Description":       description,
		"RequireSetupToken": action == "/admin/setup",
		"CSRFToken":         issueAdminCSRFToken(w, nil),
	})
}

func (a *app) renderAdminResetForm(w http.ResponseWriter, errorMessage string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = a.adminReset.Execute(w, map[string]any{
		"Title":       a.i18n.T("admin_reset_title"),
		"Error":       errorMessage,
		"Action":      "/admin/reset",
		"Button":      a.i18n.T("admin_reset_button"),
		"Description": a.i18n.T("admin_reset_description"),
		"CSRFToken":   issueAdminCSRFToken(w, nil),
	})
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
	entries map[string][]time.Time
}

func newAuthAttemptLimiter(window time.Duration, limit int) *authAttemptLimiter {
	return &authAttemptLimiter{
		window:  window,
		limit:   limit,
		entries: make(map[string][]time.Time),
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
	l.entries[key] = append(attempts, now)
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
	attempts := l.entries[key]
	if len(attempts) == 0 {
		return nil
	}

	cutoff := now.Add(-l.window)
	keep := attempts[:0]
	for _, at := range attempts {
		if at.After(cutoff) {
			keep = append(keep, at)
		}
	}

	if len(keep) == 0 {
		delete(l.entries, key)
		return nil
	}

	l.entries[key] = keep
	return keep
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

var errUploadTooLarge = errors.New("upload too large")

func spoolUploadToTemp(reader io.Reader, maxBytes int64, sampleLimit int) (*os.File, []byte, error) {
	tmp, err := os.CreateTemp("", "pastebox-upload-*")
	if err != nil {
		return nil, nil, err
	}
	buffered := bufio.NewReader(reader)
	sample := make([]byte, 0, sampleLimit)
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, readErr := buffered.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > maxBytes {
				_ = tmp.Close()
				_ = os.Remove(tmp.Name())
				return nil, nil, errUploadTooLarge
			}
			if _, err := tmp.Write(buf[:n]); err != nil {
				_ = tmp.Close()
				_ = os.Remove(tmp.Name())
				return nil, nil, err
			}
			if len(sample) < sampleLimit {
				need := sampleLimit - len(sample)
				if need > n {
					need = n
				}
				sample = append(sample, buf[:need]...)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return nil, nil, readErr
		}
	}
	return tmp, sample, nil
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

func allowTextUpload(filename string, contentType string, content []byte) (bool, string) {
	ext := normalizedUploadExt(filename)
	lowerContentType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))

	if isBlockedUploadExtension(ext) {
		return false, "blocked extension"
	}

	if isBlockedUploadContentType(lowerContentType) {
		if lowerContentType == "application/octet-stream" {
			if looksLikeText(content) {
				return true, ""
			}
			return false, "octet-stream binary content"
		}
		return false, "blocked content type"
	}

	if isTextContentType(lowerContentType) {
		if looksLikeText(content) {
			return true, ""
		}
		return false, "text content type but binary content"
	}

	if looksLikeText(content) {
		return true, ""
	}

	return false, "not text"
}

func normalizeTextContentType(filename string, contentType string) string {
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

func isBlockedUploadExtension(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".bmp", ".svg", ".gif", ".webp", ".ico", ".tif", ".tiff",
		".mp4", ".mp3", ".mpv", ".mkv", ".mov", ".avi", ".wmv", ".flv", ".webm", ".m4v",
		".wav", ".flac", ".aac", ".ogg", ".m4a",
		".iso", ".zip", ".tar", ".tar.gz", ".tgz", ".tar.xz", ".txz", ".tar.bz2", ".tbz2",
		".gz", ".xz", ".bz2", ".7z", ".rar",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".exe", ".dll", ".so", ".dylib", ".bin", ".img", ".apk", ".deb", ".rpm":
		return true
	default:
		return false
	}
}

func isBlockedUploadContentType(contentType string) bool {
	if contentType == "" {
		return false
	}

	if strings.HasPrefix(contentType, "image/") ||
		strings.HasPrefix(contentType, "video/") ||
		strings.HasPrefix(contentType, "audio/") {
		return true
	}

	switch contentType {
	case "application/zip",
		"application/x-zip-compressed",
		"application/x-tar",
		"application/gzip",
		"application/x-gzip",
		"application/x-7z-compressed",
		"application/vnd.rar",
		"application/x-rar-compressed",
		"application/x-iso9660-image",
		"application/pdf",
		"application/octet-stream":
		return true
	default:
		return false
	}
}

func isTextContentType(contentType string) bool {
	if contentType == "" {
		return false
	}

	if strings.HasPrefix(contentType, "text/") {
		return true
	}

	if strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "yaml") ||
		strings.Contains(contentType, "toml") ||
		strings.Contains(contentType, "javascript") ||
		strings.Contains(contentType, "ecmascript") ||
		strings.Contains(contentType, "x-sh") ||
		strings.Contains(contentType, "x-shellscript") {
		return true
	}

	return false
}

func syntaxLanguage(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))

	switch {
	case strings.Contains(contentType, "x-log"):
		return "logs"
	case strings.Contains(contentType, "yaml"):
		return "yaml"
	case strings.Contains(contentType, "x-ini"):
		return "ini"
	case strings.Contains(contentType, "x-rust"):
		return "rust"
	case strings.Contains(contentType, "x-go"):
		return "go"
	case strings.Contains(contentType, "javascript"):
		return "javascript"
	case strings.Contains(contentType, "x-python"):
		return "python"
	case strings.Contains(contentType, "markdown"):
		return "markdown"
	case strings.Contains(contentType, "typescript"):
		return "typescript"
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

func isBrowserRequest(r *http.Request) bool {
	ua := strings.ToLower(r.UserAgent())
	if strings.HasPrefix(ua, "curl/") || strings.Contains(ua, "wget/") || strings.Contains(ua, "httpie/") {
		return false
	}

	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "text/html") || accept == ""
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

func (a *app) respondRequestError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if responseFormat(r) == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = writePrettyJSON(w, map[string]string{
			"error": message,
		})
		return
	}

	http.Error(w, message, status)
}

func writePrettyJSON(w http.ResponseWriter, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

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

	pos, _ := entry.File.Seek(0, io.SeekCurrent)

	buf := make([]byte, 4096)
	n, _ := entry.File.Read(buf)
	_, _ = entry.File.Seek(pos, io.SeekStart)

	return looksLikeText(buf[:n])
}

func looksLikeText(buf []byte) bool {
	if len(buf) == 0 {
		return true
	}

	if bytes.IndexByte(buf, 0) >= 0 {
		return false
	}

	if !utf8.Valid(buf) {
		return false
	}

	bad := 0
	for _, b := range buf {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			bad++
		}
	}

	return bad == 0
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

type localizer struct {
	language string
	messages map[string]string
}

func loadLocalizer(language string) *localizer {
	language = normalizeLanguage(language)

	messages := map[string]string{}
	loadMessages(messages, "locales/en.json", false)

	if language != "en" {
		loadMessages(messages, "locales/"+language+".json", true)
	}

	return &localizer{
		language: language,
		messages: messages,
	}
}

func normalizeLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "ko", "en":
		return strings.ToLower(strings.TrimSpace(language))
	default:
		return "en"
	}
}

func loadMessages(messages map[string]string, path string, optional bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		if optional {
			log.Printf("translation file not loaded: %s: %v", path, err)
			return
		}

		log.Fatalf("failed to read translation file %s: %v", path, err)
	}

	var loaded map[string]string
	if err := json.Unmarshal(data, &loaded); err != nil {
		if optional {
			log.Printf("translation file not parsed: %s: %v", path, err)
			return
		}

		log.Fatalf("failed to parse translation file %s: %v", path, err)
	}

	for key, value := range loaded {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		messages[key] = value
	}
}

func (l *localizer) T(key string) string {
	if l == nil {
		return key
	}

	if value := strings.TrimSpace(l.messages[key]); value != "" {
		return value
	}

	return key
}

func mustParseTemplate(i18n *localizer, path string) *template.Template {
	tpl, err := template.New(filepath.Base(path)).Funcs(template.FuncMap{
		"t": i18n.T,
	}).ParseFiles(path)
	if err != nil {
		log.Fatalf("failed to parse template %s: %v", path, err)
	}

	return tpl
}

func getenv(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return n
}
