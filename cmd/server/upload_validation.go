package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

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
