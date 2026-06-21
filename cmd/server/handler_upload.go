package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	pastebox "pastebox/internal"
)

func (a *app) uploadHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	disabled, err := a.store.UploadsDisabled()
	if err != nil {
		logEvent("uploads.status_read_failed", map[string]any{
			"error": err,
		})
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
		logEvent("upload.blocked", map[string]any{
			"content_type": contentType,
			"filename":     filename,
			"reason":       reason,
			"remote":       r.RemoteAddr,
		})
		a.respondRequestError(w, r, http.StatusUnsupportedMediaType, "unsupported file type. only text-based files are allowed")
		return
	}

	contentType = normalizeTextContentType(filename, contentType)

	usePassword := strings.EqualFold(strings.TrimSpace(r.Header.Get("usepassword")), "true")
	policy, err := pastebox.ParseDataPolicy(r.Header.Get("data-policy"))
	if err != nil {
		a.respondRequestError(w, r, http.StatusBadRequest, "invalid data-policy. use temporary, permanent, once, or a duration up to 30d like 30m, 12h, 7d")
		return
	}
	customCode := strings.TrimSpace(r.Header.Get("code"))

	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		a.respondRequestError(w, r, http.StatusInternalServerError, "failed to process upload")
		return
	}

	meta, password, deleteToken, manageToken, err := a.store.Create(tempFile, filename, contentType, usePassword, policy, customCode)
	if err != nil {
		logEvent("upload.create_failed", map[string]any{
			"error": err,
		})

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

	logEvent("paste.created", map[string]any{
		"content_type": meta.ContentType,
		"expires":      formatExpiresForLog(meta),
		"id":           meta.ID,
		"policy":       meta.DataPolicy,
		"protected":    password != "",
		"remote":       r.RemoteAddr,
		"size":         meta.Size,
	})

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
