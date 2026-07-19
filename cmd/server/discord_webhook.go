package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	pastebox "pastebox/internal"
)

const discordWebhookTimeout = 5 * time.Second
const discordWebhookQueueSize = 64
const discordWebhookWorkers = 2

type discordWebhookNotifier struct {
	url    string
	client *http.Client
	queue  chan discordNotification
}

type discordNotification struct {
	event discordPasteEvent
	i18n  *localizer
}

type discordPasteEvent struct {
	Action    string
	Code      string
	Source    string
	Filename  string
	Policy    string
	Size      int64
	Protected bool
	ExpiresAt time.Time
	URL       string
	Trigger   string
	Count     int
	CreatedAt time.Time
}

type discordWebhookPayload struct {
	Username string         `json:"username,omitempty"`
	Embeds   []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	URL         string              `json:"url,omitempty"`
	Color       int                 `json:"color"`
	Author      discordEmbedAuthor  `json:"author,omitempty"`
	Fields      []discordEmbedField `json:"fields,omitempty"`
	Timestamp   string              `json:"timestamp,omitempty"`
}

type discordEmbedAuthor struct {
	Name string `json:"name,omitempty"`
}

type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

func newDiscordWebhookNotifier(webhookURL string) *discordWebhookNotifier {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return nil
	}

	notifier := &discordWebhookNotifier{
		url: webhookURL,
		client: &http.Client{
			Timeout: discordWebhookTimeout,
		},
		queue: make(chan discordNotification, discordWebhookQueueSize),
	}
	for range discordWebhookWorkers {
		go notifier.runWorker()
	}
	return notifier
}

func (n *discordWebhookNotifier) runWorker() {
	for notification := range n.queue {
		ctx, cancel := context.WithTimeout(context.Background(), discordWebhookTimeout)
		err := n.notify(ctx, notification.event, notification.i18n)
		cancel()
		if err != nil {
			logEvent("discord.webhook_failed", map[string]any{
				"error": err,
				"event": notification.event.Action,
			})
		}
	}
}

func (n *discordWebhookNotifier) enqueue(event discordPasteEvent, i18n *localizer) bool {
	select {
	case n.queue <- discordNotification{event: event, i18n: i18n}:
		return true
	default:
		return false
	}
}

func (n *discordWebhookNotifier) notify(ctx context.Context, event discordPasteEvent, i18n *localizer) error {
	if n == nil || strings.TrimSpace(n.url) == "" {
		return nil
	}

	payload := buildDiscordWebhookPayload(event, i18n)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned status %d", resp.StatusCode)
	}

	return nil
}

func (a *app) notifyDiscordPasteEvent(event discordPasteEvent) {
	if a.discordWebhook == nil {
		return
	}

	if !a.discordWebhook.enqueue(event, a.i18n) {
		logEvent("discord.webhook_dropped", map[string]any{
			"event":  event.Action,
			"reason": "queue_full",
		})
	}
}

func (a *app) notifyDiscordPasteCreated(r *http.Request, meta pastebox.Metadata, protected bool, source string) {
	a.notifyDiscordPasteEvent(discordPasteEvent{
		Action:    "created",
		Code:      meta.ID,
		Source:    source,
		Filename:  meta.Filename,
		Policy:    meta.DataPolicy,
		Size:      meta.Size,
		Protected: protected,
		ExpiresAt: meta.ExpiresAt,
		URL:       strings.TrimRight(requestBaseURL(r), "/") + "/" + meta.ID,
		CreatedAt: meta.CreatedAt,
	})
}

func (a *app) notifyDiscordPasteDeleted(id string, trigger string) {
	a.notifyDiscordPasteEvent(discordPasteEvent{
		Action:  "deleted",
		Code:    id,
		Trigger: trigger,
	})
}

func (a *app) notifyDiscordPastesDeleted(count int, trigger string) {
	a.notifyDiscordPasteEvent(discordPasteEvent{
		Action:  "deleted_bulk",
		Count:   count,
		Trigger: trigger,
	})
}

func buildDiscordWebhookPayload(event discordPasteEvent, i18n *localizer) discordWebhookPayload {
	embed := discordEmbed{
		Title:     discordText(i18n, discordTitleKey(event), discordFallbackTitle(event)),
		Color:     discordEventColor(event),
		Author:    discordEmbedAuthor{Name: "Pastebox"},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if !event.CreatedAt.IsZero() {
		embed.Timestamp = event.CreatedAt.UTC().Format(time.RFC3339)
	}

	switch event.Action {
	case "created":
		embed.Description = discordText(i18n, discordDescriptionKey(event), discordFallbackDescription(event))
		embed.Fields = append(embed.Fields,
			discordField(discordText(i18n, "discord_field_code", "Code"), event.Code, true),
			discordField(discordText(i18n, "discord_field_policy", "Policy"), displayValue(event.Policy, "temporary"), true),
			discordField(discordText(i18n, "discord_field_protected", "Protected"), localizedYesNo(i18n, event.Protected), true),
		)
		if event.Source != "" {
			embed.Fields = append(embed.Fields, discordField(discordText(i18n, "discord_field_source", "Source"), event.Source, true))
		}
		embed.Fields = append(embed.Fields, discordField(discordText(i18n, "discord_field_size", "Size"), formatBytes(event.Size), true))
		if event.Filename != "" {
			embed.Fields = append(embed.Fields, discordField(discordText(i18n, "discord_field_filename", "Filename"), event.Filename, true))
		}
		if !event.ExpiresAt.IsZero() {
			embed.Fields = append(embed.Fields, discordField(discordText(i18n, "discord_field_expires", "Expires"), event.ExpiresAt.In(time.Local).Format(time.RFC3339), true))
		}
	case "deleted":
		embed.Description = discordText(i18n, "discord_desc_deleted", "A paste was deleted.")
		embed.Fields = append(embed.Fields,
			discordField(discordText(i18n, "discord_field_code", "Code"), event.Code, true),
			discordField(discordText(i18n, "discord_field_trigger", "Trigger"), displayValue(event.Trigger, "delete"), true),
		)
	case "deleted_bulk":
		embed.Description = discordText(i18n, "discord_desc_deleted_bulk", "Multiple pastes were deleted.")
		embed.Fields = append(embed.Fields,
			discordField(discordText(i18n, "discord_field_count", "Count"), fmt.Sprint(event.Count), true),
			discordField(discordText(i18n, "discord_field_trigger", "Trigger"), displayValue(event.Trigger, "admin"), true),
		)
	}

	return discordWebhookPayload{
		Username: "Pastebox",
		Embeds:   []discordEmbed{embed},
	}
}

func discordTitleKey(event discordPasteEvent) string {
	switch event.Action {
	case "created":
		if event.Source != "" {
			return "discord_title_cloned"
		}
		return "discord_title_created"
	case "deleted":
		return "discord_title_deleted"
	case "deleted_bulk":
		return "discord_title_deleted_bulk"
	default:
		return "discord_title_event"
	}
}

func discordFallbackTitle(event discordPasteEvent) string {
	switch event.Action {
	case "created":
		if event.Source != "" {
			return "Paste cloned"
		}
		return "Paste created"
	case "deleted":
		return "Paste deleted"
	case "deleted_bulk":
		return "Pastes deleted"
	default:
		return "Pastebox event"
	}
}

func discordDescriptionKey(event discordPasteEvent) string {
	if event.Action == "created" && event.Source != "" {
		return "discord_desc_cloned"
	}
	return "discord_desc_created"
}

func discordFallbackDescription(event discordPasteEvent) string {
	if event.Action == "created" && event.Source != "" {
		return "A paste was cloned into a new link."
	}
	return "A new paste was created."
}

func discordEventColor(event discordPasteEvent) int {
	switch event.Action {
	case "created":
		if event.Source != "" {
			return 0xb8d8b3
		}
		return 0xa8dadc
	case "deleted":
		return 0xffc1cc
	case "deleted_bulk":
		return 0xf0c674
	default:
		return 0xb39cd0
	}
}

func discordField(name string, value string, inline bool) discordEmbedField {
	return discordEmbedField{
		Name:   name,
		Value:  truncateDiscordFieldValue(displayValue(value, "-")),
		Inline: inline,
	}
}

func displayValue(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func localizedYesNo(i18n *localizer, value bool) string {
	if value {
		return discordText(i18n, "discord_value_yes", "yes")
	}
	return discordText(i18n, "discord_value_no", "no")
}

func discordText(i18n *localizer, key string, fallback string) string {
	if i18n == nil {
		return fallback
	}
	value := strings.TrimSpace(i18n.T(key))
	if value == "" || value == key {
		return fallback
	}
	return value
}

func truncateDiscordFieldValue(value string) string {
	const maxDiscordFieldValue = 1024
	runes := []rune(value)
	if len(runes) <= maxDiscordFieldValue {
		return value
	}
	return string(runes[:maxDiscordFieldValue-3]) + "..."
}
