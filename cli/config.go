package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type config struct {
	ServerURL string
}

const initialConfig = "{\n  \"server_url\": \"\"\n}\n"

func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".config", "pastebox", "config.json")
	}
	return filepath.Join(home, ".config", "pastebox", "config.json")
}

func initializeConfig(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("cannot inspect config %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("cannot create config directory %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("cannot create config %s: %w", path, err)
	}
	if _, err := file.WriteString(initialConfig); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return false, fmt.Errorf("cannot write config %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("cannot close config %s: %w", path, err)
	}
	return true, nil
}

func loadConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config{}, fmt.Errorf("config file not found: %s\n\nRun pb without arguments to create it, then set server_url before use", path)
		}
		return config{}, fmt.Errorf("cannot read config %s: %w", path, err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return config{}, jsonConfigError(path, data, err)
	}
	if fields == nil {
		return config{}, fmt.Errorf("invalid config %s: expected a JSON object", path)
	}

	unknown := make([]string, 0)
	for field := range fields {
		if field != "server_url" {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return config{}, fmt.Errorf("invalid config %s: unknown field %q; expected \"server_url\"", path, unknown[0])
	}

	rawURL, ok := fields["server_url"]
	if !ok {
		return config{}, fmt.Errorf("invalid config %s: required field \"server_url\" is missing", path)
	}
	var serverURL string
	if err := json.Unmarshal(rawURL, &serverURL); err != nil {
		return config{}, fmt.Errorf("invalid config %s: server_url must be a string", path)
	}
	serverURL = strings.TrimSpace(serverURL)
	if serverURL == "" {
		return config{}, fmt.Errorf("invalid config %s: server_url must not be empty", path)
	}

	parsed, err := url.Parse(serverURL)
	if err != nil {
		return config{}, fmt.Errorf("invalid config %s: server_url %q is not a valid URL: %v", path, serverURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		if parsed.Scheme == "" {
			return config{}, fmt.Errorf("invalid config %s: server_url %q is missing http:// or https://", path, serverURL)
		}
		return config{}, fmt.Errorf("invalid config %s: server_url must use http:// or https://, not %s://", path, parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return config{}, fmt.Errorf("invalid config %s: server_url must include a host", path)
	}
	if parsed.User != nil {
		return config{}, fmt.Errorf("invalid config %s: server_url must not contain user credentials", path)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return config{}, fmt.Errorf("invalid config %s: server_url must not contain a query or fragment", path)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return config{ServerURL: parsed.String()}, nil
}

func jsonConfigError(path string, data []byte, err error) error {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		line, column := lineColumn(data, syntaxErr.Offset)
		return fmt.Errorf("invalid config %s:%d:%d: %s", path, line, column, syntaxErr.Error())
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		line, column := lineColumn(data, typeErr.Offset)
		return fmt.Errorf("invalid config %s:%d:%d: %s", path, line, column, typeErr.Error())
	}
	return fmt.Errorf("invalid config %s: %v", path, err)
}

func lineColumn(data []byte, offset int64) (int, int) {
	if offset < 1 {
		return 1, 1
	}
	index := int(offset - 1)
	if index > len(data) {
		index = len(data)
	}
	line := bytes.Count(data[:index], []byte{'\n'}) + 1
	lastNewline := bytes.LastIndexByte(data[:index], '\n')
	return line, index - lastNewline
}
