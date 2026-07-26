package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestReleaseCheckerReturnsAndCachesLatestRelease(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v26.07.26","html_url":"https://github.com/kmw0410/pastebox/releases/tag/v26.07.26"}`))
	}))
	defer server.Close()

	checker := newReleaseChecker("v26.07.25")
	checker.client = server.Client()
	checker.endpoint = server.URL

	first := checker.Check(context.Background())
	second := checker.Check(context.Background())

	if first.Current != "v26.07.25" || first.Latest != "v26.07.26" {
		t.Fatalf("unexpected release status: %#v", first)
	}
	if !first.UpdateAvailable || first.CheckFailed || first.Development {
		t.Fatalf("unexpected release flags: %#v", first)
	}
	if second != first {
		t.Fatalf("cached status = %#v, want %#v", second, first)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestReleaseCheckerHandlesDevelopmentAndFailure(t *testing.T) {
	t.Run("development", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"tag_name":"v26.07.26","html_url":"https://github.com/kmw0410/pastebox/releases/tag/v26.07.26"}`))
		}))
		defer server.Close()

		checker := newReleaseChecker("")
		checker.client = server.Client()
		checker.endpoint = server.URL

		status := checker.Check(context.Background())
		if status.Current != "development" || !status.Development || status.UpdateAvailable {
			t.Fatalf("unexpected development status: %#v", status)
		}
	})

	t.Run("failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "rate limited", http.StatusForbidden)
		}))
		defer server.Close()

		checker := newReleaseChecker("v26.07.25")
		checker.client = server.Client()
		checker.endpoint = server.URL

		status := checker.Check(context.Background())
		if !status.CheckFailed || status.Latest != "" || status.UpdateAvailable {
			t.Fatalf("unexpected failure status: %#v", status)
		}
	})
}

func TestReleaseVersionLess(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{name: "new day", current: "v26.07.25", latest: "v26.07.26", want: true},
		{name: "new suffix", current: "v26.07.26", latest: "v26.07.26-1", want: true},
		{name: "same", current: "v26.07.26-1", latest: "v26.07.26-1", want: false},
		{name: "current ahead of release workflow", current: "v26.07.26", latest: "v26.07.25", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := releaseVersionLess(tt.current, tt.latest); got != tt.want {
				t.Fatalf("releaseVersionLess(%q, %q) = %t, want %t", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}
