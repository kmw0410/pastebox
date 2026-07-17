package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

type uploadOptions struct {
	permanent   bool
	once        bool
	expires     string
	usePassword bool
	code        string
	label       string
	quiet       bool
	jsonOutput  bool
}

type uploadResponse struct {
	URL      string `json:"url"`
	Expires  string `json:"expires,omitempty"`
	Password string `json:"password,omitempty"`
	Manage   string `json:"manage,omitempty"`
	Delete   string `json:"delete,omitempty"`
}

func (o uploadOptions) validate() error {
	policies := 0
	if o.permanent {
		policies++
	}
	if o.once {
		policies++
	}
	if strings.TrimSpace(o.expires) != "" {
		policies++
	}
	if policies > 1 {
		return fmt.Errorf("--permanent, --once, and --expires are mutually exclusive")
	}
	if o.quiet && o.jsonOutput {
		return fmt.Errorf("--quiet and --json are mutually exclusive")
	}
	return nil
}

func (o uploadOptions) policy() string {
	switch {
	case o.permanent:
		return "permanent"
	case o.once:
		return "once"
	case strings.TrimSpace(o.expires) != "":
		return strings.TrimSpace(o.expires)
	default:
		return ""
	}
}

func upload(client *http.Client, cfg config, input io.Reader, filename string, opts uploadOptions) (uploadResponse, []byte, error) {
	endpoint := strings.TrimRight(cfg.ServerURL, "/") + "/?format=json"
	var body io.Reader = input
	contentType := "text/plain; charset=utf-8"
	if filename != "" {
		pipeReader, writer := io.Pipe()
		body = pipeReader
		multipartWriter := multipart.NewWriter(writer)
		contentType = multipartWriter.FormDataContentType()
		go func() {
			part, err := multipartWriter.CreateFormFile("file", filepath.Base(filename))
			if err == nil {
				_, err = io.Copy(part, input)
			}
			if closeErr := multipartWriter.Close(); err == nil {
				err = closeErr
			}
			_ = writer.CloseWithError(err)
		}()
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, body)
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("create upload request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	if policy := opts.policy(); policy != "" {
		req.Header.Set("data-policy", policy)
	}
	if opts.usePassword {
		req.Header.Set("usepassword", "true")
	}
	if opts.code != "" {
		req.Header.Set("code", opts.code)
	}
	if opts.label != "" {
		req.Header.Set("label", opts.label)
	}

	resp, err := client.Do(req)
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("request failed for %s: %w", cfg.ServerURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("read upload response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return uploadResponse{}, nil, responseError("upload failed", resp, raw)
	}

	var result uploadResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return uploadResponse{}, nil, fmt.Errorf("invalid upload response from server: %w", err)
	}
	if strings.TrimSpace(result.URL) == "" {
		return uploadResponse{}, nil, fmt.Errorf("invalid upload response from server: missing url")
	}
	return result, raw, nil
}

func responseError(prefix string, resp *http.Response, body []byte) error {
	message := ""
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil {
		message = strings.TrimSpace(payload.Error)
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("%s: %s (HTTP %d)", prefix, message, resp.StatusCode)
}

func addRawQuery(target string) (string, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("raw", "1")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
