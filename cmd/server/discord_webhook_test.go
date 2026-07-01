package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBuildDiscordWebhookPayloadForCreatedPaste(t *testing.T) {
	expiresAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	payload := buildDiscordWebhookPayload(discordPasteEvent{
		Action:    "created",
		Code:      "abc12",
		Filename:  "server.log",
		Policy:    "12h",
		Size:      1536,
		Protected: true,
		ExpiresAt: expiresAt,
		URL:       "https://paste.example/abc12",
		CreatedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
	}, nil)

	if payload.Username != "Pastebox" {
		t.Fatalf("Username = %q, want Pastebox", payload.Username)
	}
	if len(payload.Embeds) != 1 {
		t.Fatalf("Embeds length = %d, want 1", len(payload.Embeds))
	}

	embed := payload.Embeds[0]
	if embed.Title != "Paste created" {
		t.Fatalf("Title = %q, want Paste created", embed.Title)
	}
	if embed.Author.Name != "Pastebox" {
		t.Fatalf("Author.Name = %q, want Pastebox", embed.Author.Name)
	}
	if embed.Timestamp != "2026-07-01T12:00:00Z" {
		t.Fatalf("Timestamp = %q, want created timestamp", embed.Timestamp)
	}

	fields := discordFieldsByName(embed.Fields)
	if fields["Code"] != "abc12" {
		t.Fatalf("Code field = %q, want abc12", fields["Code"])
	}
	if fields["Filename"] != "server.log" {
		t.Fatalf("Filename field = %q, want server.log", fields["Filename"])
	}
	if fields["Policy"] != "12h" {
		t.Fatalf("Policy field = %q, want 12h", fields["Policy"])
	}
	if fields["Protected"] != "yes" {
		t.Fatalf("Protected field = %q, want yes", fields["Protected"])
	}
	if fields["Size"] != "1.5 KiB" {
		t.Fatalf("Size field = %q, want 1.5 KiB", fields["Size"])
	}
	if fields["Expires"] == "" {
		t.Fatalf("Expires field is empty")
	}
}

func TestBuildDiscordWebhookPayloadUsesKoreanLocale(t *testing.T) {
	i18n := &localizer{language: "ko", messages: map[string]string{
		"discord_title_created":   "Paste 생성됨",
		"discord_desc_created":    "새 Paste가 생성되었습니다.",
		"discord_field_code":      "코드",
		"discord_field_policy":    "정책",
		"discord_field_protected": "보호 여부",
		"discord_field_size":      "크기",
		"discord_value_yes":       "예",
	}}
	payload := buildDiscordWebhookPayload(discordPasteEvent{
		Action:    "created",
		Code:      "abc12",
		Policy:    "temporary",
		Size:      100,
		Protected: true,
	}, i18n)

	embed := payload.Embeds[0]
	if embed.Title != "Paste 생성됨" {
		t.Fatalf("Title = %q, want Korean title", embed.Title)
	}
	if embed.Description != "새 Paste가 생성되었습니다." {
		t.Fatalf("Description = %q, want Korean description", embed.Description)
	}

	fields := discordFieldsByName(embed.Fields)
	if fields["코드"] != "abc12" {
		t.Fatalf("Korean code field = %q, want abc12", fields["코드"])
	}
	if fields["보호 여부"] != "예" {
		t.Fatalf("Korean protected field = %q, want 예", fields["보호 여부"])
	}
}

func TestDiscordWebhookNotifierPostsEmbed(t *testing.T) {
	var gotPayload discordWebhookPayload
	notifier := &discordWebhookNotifier{
		url: "https://discord.example/webhook",
		client: &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", got)
				}

				if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
					t.Errorf("Decode failed: %v", err)
				}
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Body:       io.NopCloser(bytes.NewReader(nil)),
				}, nil
			}),
		},
	}

	err := notifier.notify(context.Background(), discordPasteEvent{
		Action:  "deleted",
		Code:    "abc12",
		Trigger: "admin",
	}, nil)
	if err != nil {
		t.Fatalf("notify failed: %v", err)
	}

	if len(gotPayload.Embeds) != 1 || gotPayload.Embeds[0].Title != "Paste deleted" {
		t.Fatalf("unexpected payload: %+v", gotPayload)
	}
	body, err := json.Marshal(gotPayload)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if strings.Contains(string(body), "delete=") || strings.Contains(string(body), "manage=") || strings.Contains(string(body), "password") {
		t.Fatalf("payload contains secret-like data: %s", string(body))
	}
}

func discordFieldsByName(fields []discordEmbedField) map[string]string {
	out := make(map[string]string, len(fields))
	for _, field := range fields {
		out[field.Name] = field.Value
	}
	return out
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
