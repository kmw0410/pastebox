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
	parts := []string{fmt.Sprintf("event=%s", event)}
	for _, key := range sortedKeys(fields) {
		value := fields[key]
		if err, ok := value.(error); ok && err != nil {
			parts = append(parts, fmt.Sprintf("%s=%q", key, err.Error()))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%q", key, fmt.Sprint(value)))
	}
	return strings.Join(parts, " ")
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
