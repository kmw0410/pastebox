package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const latestReleaseAPI = "https://api.github.com/repos/kmw0410/pastebox/releases/latest"

var version = "development"

type releaseStatus struct {
	Current         string
	Latest          string
	ReleaseURL      string
	UpdateAvailable bool
	Development     bool
	CheckFailed     bool
}

type releaseChecker struct {
	client   *http.Client
	endpoint string
	current  string

	mu        sync.Mutex
	cached    releaseStatus
	expiresAt time.Time
}

func newReleaseChecker(current string) *releaseChecker {
	current = strings.TrimSpace(current)
	if current == "" {
		current = "development"
	}

	return &releaseChecker{
		client:   &http.Client{Timeout: 3 * time.Second},
		endpoint: latestReleaseAPI,
		current:  current,
	}
}

func (c *releaseChecker) Check(ctx context.Context) releaseStatus {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	if now.Before(c.expiresAt) {
		return c.cached
	}

	status := releaseStatus{
		Current:     c.current,
		Development: c.current == "development",
	}

	latest, releaseURL, err := fetchLatestRelease(ctx, c.client, c.endpoint)
	if err != nil {
		status.CheckFailed = true
		c.expiresAt = now.Add(time.Minute)
	} else {
		status.Latest = latest
		status.ReleaseURL = releaseURL
		status.UpdateAvailable = !status.Development && releaseVersionLess(status.Current, status.Latest)
		c.expiresAt = now.Add(15 * time.Minute)
	}

	c.cached = status
	return status
}

func releaseVersionLess(current, latest string) bool {
	currentParts, currentOK := parseReleaseVersion(current)
	latestParts, latestOK := parseReleaseVersion(latest)
	if !currentOK || !latestOK {
		return current != latest
	}

	for i := range currentParts {
		if currentParts[i] != latestParts[i] {
			return currentParts[i] < latestParts[i]
		}
	}
	return false
}

func parseReleaseVersion(value string) ([4]int, bool) {
	var parsed [4]int
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	main, suffix, hasSuffix := strings.Cut(value, "-")
	parts := strings.Split(main, ".")
	if len(parts) != 3 {
		return parsed, false
	}

	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return parsed, false
		}
		parsed[i] = number
	}

	if hasSuffix {
		number, err := strconv.Atoi(suffix)
		if err != nil || number < 1 {
			return parsed, false
		}
		parsed[3] = number
	}

	return parsed, true
}

func fetchLatestRelease(ctx context.Context, client *http.Client, endpoint string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "pastebox/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", errors.New("latest release request failed")
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", err
	}

	release.TagName = strings.TrimSpace(release.TagName)
	release.HTMLURL = strings.TrimSpace(release.HTMLURL)
	if release.TagName == "" || release.HTMLURL == "" {
		return "", "", errors.New("latest release response is incomplete")
	}

	return release.TagName, release.HTMLURL, nil
}
