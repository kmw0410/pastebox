package main

import (
	"encoding/json"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
)

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
