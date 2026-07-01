package main

import (
	"fmt"
	"log"
	"sort"
	"strings"
)

func logEvent(event string, fields map[string]any) {
	log.Printf("%s", formatLogEvent(event, fields))
}

func logFatalEvent(event string, fields map[string]any) {
	log.Fatal(formatLogEvent(event, fields))
}

func formatLogEvent(event string, fields map[string]any) string {
	switch event {
	case "server.started":
		return fmt.Sprintf("pastebox listening on %s, data=%s", field(fields, "listen_addr"), field(fields, "data_dir"))
	case "server.storage_backend":
		if field(fields, "storage_backend") == "mysql" {
			return fmt.Sprintf("storage backend: mysql, zstd level=%s", field(fields, "zstd_level"))
		}
		return fmt.Sprintf("storage backend: %s", field(fields, "storage_backend"))
	case "server.init_store_failed":
		return fmt.Sprintf("failed to initialize store: %s", field(fields, "error"))
	case "server.init_app_failed":
		return fmt.Sprintf("failed to initialize app: %s", field(fields, "error"))
	case "server.listen_failed":
		return field(fields, "error")
	case "store.cleanup_failed":
		return fmt.Sprintf("cleanup failed: %s", field(fields, "error"))
	case "store.health_unhealthy":
		return fmt.Sprintf("store health check failed: backend=%s error=%s", field(fields, "storage_backend"), field(fields, "error"))
	case "store.health_recovered":
		return fmt.Sprintf("store health recovered: backend=%s", field(fields, "storage_backend"))
	case "admin.setup_token_generated":
		return fmt.Sprintf("admin setup token: %s", field(fields, "token"))
	case "uploads.status_read_failed":
		return fmt.Sprintf("failed to read upload status: %s", field(fields, "error"))
	case "upload.blocked":
		return fmt.Sprintf("upload blocked: remote=%s filename=%q content_type=%q reason=%s", field(fields, "remote"), field(fields, "filename"), field(fields, "content_type"), field(fields, "reason"))
	case "upload.create_failed":
		return fmt.Sprintf("upload failed: %s", field(fields, "error"))
	case "paste.created":
		return fmt.Sprintf("created: id=%s remote=%s size=%s content_type=%q policy=%s expires=%s protected=%s", field(fields, "id"), field(fields, "remote"), field(fields, "size"), field(fields, "content_type"), field(fields, "policy"), field(fields, "expires"), field(fields, "protected"))
	case "paste.cloned":
		return fmt.Sprintf("cloned: source=%s id=%s remote=%s size=%s content_type=%q policy=%s expires=%s protected=%s", field(fields, "source"), field(fields, "id"), field(fields, "remote"), field(fields, "size"), field(fields, "content_type"), field(fields, "policy"), field(fields, "expires"), field(fields, "protected"))
	case "paste.delete_denied":
		return fmt.Sprintf("delete denied: id=%s remote=%s", field(fields, "id"), field(fields, "remote"))
	case "paste.deleted":
		return fmt.Sprintf("deleted: id=%s remote=%s", field(fields, "id"), field(fields, "remote"))
	case "discord.webhook_failed":
		return fmt.Sprintf("discord webhook failed: event=%s error=%s", field(fields, "event"), field(fields, "error"))
	case "admin.audit":
		return "admin audit: " + formatFields(fields)
	case "admin.csrf_validation_failed":
		return fmt.Sprintf("csrf validation failed: path=%s remote=%s", field(fields, "path"), field(fields, "remote"))
	case "admin.created":
		return fmt.Sprintf("admin created: username=%s remote=%s", field(fields, "username"), field(fields, "remote"))
	case "admin.login_rate_limited":
		return fmt.Sprintf("admin login rate limited: ip=%s remote=%s retry_after=%s", field(fields, "client_ip"), field(fields, "remote"), field(fields, "retry_after"))
	case "admin.login_failed":
		return fmt.Sprintf("admin login failed: username=%s remote=%s", field(fields, "username"), field(fields, "remote"))
	case "admin.login_succeeded":
		return fmt.Sprintf("admin login: username=%s remote=%s", field(fields, "username"), field(fields, "remote"))
	case "admin.password_reset":
		return fmt.Sprintf("admin password reset: remote=%s", field(fields, "remote"))
	case "i18n.translation_file_not_loaded":
		return fmt.Sprintf("translation file not loaded: %s: %s", field(fields, "path"), field(fields, "error"))
	case "i18n.translation_file_read_failed":
		return fmt.Sprintf("failed to read translation file %s: %s", field(fields, "path"), field(fields, "error"))
	case "i18n.translation_file_not_parsed":
		return fmt.Sprintf("translation file not parsed: %s: %s", field(fields, "path"), field(fields, "error"))
	case "i18n.translation_file_parse_failed":
		return fmt.Sprintf("failed to parse translation file %s: %s", field(fields, "path"), field(fields, "error"))
	case "template.parse_failed":
		return fmt.Sprintf("failed to parse template %s: %s", field(fields, "path"), field(fields, "error"))
	}

	return event + ": " + formatFields(fields)
}

func formatFields(fields map[string]any) string {
	parts := make([]string, 0, len(fields))
	for _, key := range sortedKeys(fields) {
		value := fields[key]
		if err, ok := value.(error); ok && err != nil {
			parts = append(parts, fmt.Sprintf("%s=%s", key, err.Error()))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, fmt.Sprint(value)))
	}
	return strings.Join(parts, " ")
}

func field(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}
	if err, ok := value.(error); ok && err != nil {
		return err.Error()
	}
	return fmt.Sprint(value)
}

func sortedKeys(fields map[string]any) []string {
	if len(fields) == 0 {
		return nil
	}

	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
