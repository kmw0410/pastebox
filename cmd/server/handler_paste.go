package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	pastebox "pastebox/internal"
)

const maxHTMLViewSize int64 = 10 << 20 // 10 MiB
const maxInitialHTMLViewLines = 400
const maxInitialHTMLViewBytes = 512 << 10 // 512 KiB

func (a *app) deleteHandler(w http.ResponseWriter, r *http.Request, id string, token string) {
	if r.Method == http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := a.store.Delete(id, token); err != nil {
		if errors.Is(err, pastebox.ErrInvalidDeleteToken) {
			logEvent("paste.delete_denied", map[string]any{
				"id":     id,
				"remote": r.RemoteAddr,
			})
			http.Error(w, "delete token required or invalid", http.StatusUnauthorized)
			return
		}

		http.NotFound(w, r)
		return
	}

	logEvent("paste.deleted", map[string]any{
		"id":     id,
		"remote": r.RemoteAddr,
	})
	a.notifyDiscordPasteDeleted(id, "delete link")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "deleted")
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

		oneTime := strings.EqualFold(entry.Meta.DataPolicy, "once")
		if !raw && browser && isTextEntry(entry) && (!oneTime || entry.Meta.Size <= maxHTMLViewSize) {
			var preview, remaining []byte
			var truncated bool
			var remainingLines int
			if oneTime {
				content, err := io.ReadAll(entry.File)
				if err != nil {
					return err
				}
				preview, remaining, truncated = splitPastePreview(content, maxInitialHTMLViewLines)
				if truncated {
					remainingLines = pasteLineCount(content) - pasteLineCount(preview)
				}
			} else {
				var err error
				preview, truncated, remainingLines, err = readPastePreview(entry.File, maxInitialHTMLViewLines, maxInitialHTMLViewBytes)
				if err != nil {
					return err
				}
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)

			return a.paste.Execute(w, map[string]any{
				"ID":             entry.Meta.ID,
				"Filename":       entry.Meta.Filename,
				"Label":          entry.Meta.Label,
				"Content":        string(preview),
				"Remaining":      string(remaining),
				"Truncated":      truncated,
				"RemainingLines": remainingLines,
				"OneTime":        oneTime,
				"Language":       syntaxLanguage(entry.Meta.Filename, entry.Meta.ContentType),
				"Password":       password,
				"OGTitle":        "Pastebox - " + entry.Meta.ID,
				"OGDescription":  pasteOpenGraphDescription(entry.Meta),
				"PublicURL":      strings.TrimRight(requestBaseURL(r), "/") + "/" + entry.Meta.ID,
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

func readPastePreview(reader io.Reader, maxLines int, maxBytes int64) ([]byte, bool, int, error) {
	if maxLines <= 0 || maxBytes <= 0 {
		return nil, false, 0, nil
	}

	sampleLimit := maxBytes + 1
	sample := make([]byte, 0, sampleLimit)
	buffer := make([]byte, 32*1024)
	totalBytes := int64(0)
	totalNewlines := 0
	var lastByte byte
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			chunk := buffer[:n]
			totalBytes += int64(n)
			totalNewlines += bytes.Count(chunk, []byte{'\n'})
			lastByte = chunk[n-1]
			if int64(len(sample)) < sampleLimit {
				remaining := int(sampleLimit - int64(len(sample)))
				if remaining > n {
					remaining = n
				}
				sample = append(sample, chunk[:remaining]...)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, 0, err
		}
	}

	byteTruncated := totalBytes > maxBytes
	if byteTruncated {
		sample = sample[:maxBytes]
	}

	// Decode each byte at most once. Upload validation only checks the leading
	// sample, so malformed UTF-8 can occur anywhere in this bounded preview.
	// ASCII replacements preserve the byte budget and newline counts.
	for offset := 0; offset < len(sample); {
		if byteTruncated && !utf8.FullRune(sample[offset:]) {
			sample = sample[:offset] // A rune cut by the preview byte limit.
			break
		}
		r, size := utf8.DecodeRune(sample[offset:])
		if r == utf8.RuneError && size == 1 {
			sample[offset] = '?'
		}
		offset += size
	}

	preview, _, lineTruncated := splitPastePreview(sample, maxLines)
	truncated := byteTruncated || lineTruncated
	totalLines := totalNewlines
	if totalBytes > 0 && lastByte != '\n' {
		totalLines++
	}
	remainingLines := totalLines - pasteLineCount(preview)
	if truncated && remainingLines < 1 {
		remainingLines = 1
	}
	return preview, truncated, remainingLines, nil
}

func pasteLineCount(content []byte) int {
	if len(content) == 0 {
		return 0
	}

	count := bytes.Count(content, []byte{'\n'})
	if content[len(content)-1] != '\n' {
		count++
	}
	return count
}

func splitPastePreview(content []byte, maxLines int) ([]byte, []byte, bool) {
	if maxLines <= 0 || len(content) == 0 {
		return content, nil, false
	}

	lineCount := bytes.Count(content, []byte{'\n'})
	if content[len(content)-1] != '\n' {
		lineCount++
	}
	if lineCount <= maxLines {
		return content, nil, false
	}

	separator := 0
	for line := 0; line < maxLines; line++ {
		next := bytes.IndexByte(content[separator:], '\n')
		if next < 0 {
			return content, nil, false
		}
		separator += next + 1
	}

	return content[:separator-1], content[separator-1:], true
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

	a.renderPasswordPage(w, r, id, "/"+id, http.MethodGet, a.localizedText("password_open_paste", "Open paste"))
}

func (a *app) renderPasswordPage(w http.ResponseWriter, r *http.Request, id string, action string, method string, submitLabel string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)

	_ = a.passwordPage.Execute(w, map[string]any{
		"ID":            id,
		"Action":        action,
		"Method":        method,
		"SubmitLabel":   submitLabel,
		"OGTitle":       "Pastebox - " + id,
		"OGDescription": "Password-protected paste",
		"PublicURL":     strings.TrimRight(requestBaseURL(r), "/") + "/" + id,
	})
}

func pasteOpenGraphDescription(meta pastebox.Metadata) string {
	if meta.PasswordHash != "" {
		return "Password-protected paste"
	}
	if filename := strings.TrimSpace(meta.Filename); filename != "" {
		return filename
	}
	return "Shared text paste"
}

func (a *app) localizedText(key string, fallback string) string {
	if a.i18n == nil {
		return fallback
	}

	return a.i18n.T(key)
}
