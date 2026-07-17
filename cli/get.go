package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func getPaste(client *http.Client, cfg config, target, password string, output io.Writer) error {
	requestURL, err := resolvePasteURL(cfg.ServerURL, target)
	if err != nil {
		return fmt.Errorf("invalid paste target: %w", err)
	}
	requestURL, err = addRawQuery(requestURL)
	if err != nil {
		return fmt.Errorf("invalid paste target: %w", err)
	}
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("create get request: %w", err)
	}
	if password != "" {
		req.Header.Set("paste-password", password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed for %s: %w", requestURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if readErr != nil {
			return fmt.Errorf("read paste response: %w", readErr)
		}
		return responseError("get failed", resp, body)
	}
	if _, err := io.Copy(output, resp.Body); err != nil {
		return fmt.Errorf("write paste content: %w", err)
	}
	return nil
}

func resolvePasteURL(serverURL, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("code or URL is required")
	}
	if !strings.Contains(target, "://") {
		if strings.ContainsAny(target, "/?#") {
			return "", fmt.Errorf("invalid paste code %q", target)
		}
		return strings.TrimRight(serverURL, "/") + "/" + url.PathEscape(target), nil
	}

	base, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != base.Scheme || parsed.Host != base.Host {
		return "", fmt.Errorf("paste URL must belong to configured server %s", serverURL)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("paste URL must not contain user credentials")
	}
	basePath := strings.TrimRight(base.EscapedPath(), "/")
	targetPath := parsed.EscapedPath()
	if targetPath == "" || targetPath == "/" || targetPath == basePath || targetPath == basePath+"/" {
		return "", fmt.Errorf("paste URL must include a paste code")
	}
	if basePath != "" && !strings.HasPrefix(targetPath, basePath+"/") {
		return "", fmt.Errorf("paste URL must be below configured server path %s", base.Path)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("paste URL must not contain a query or fragment")
	}
	return parsed.String(), nil
}
