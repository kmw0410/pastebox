package internal

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := NewStore(t.TempDir(), 24*time.Hour)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	t.Cleanup(func() {
		if store.adminDB != nil {
			_ = store.adminDB.Close()
		}
	})

	return store
}

func mustParsePolicy(t *testing.T, value string) DataPolicy {
	t.Helper()

	policy, err := ParseDataPolicy(value)
	if err != nil {
		t.Fatalf("ParseDataPolicy(%q) failed: %v", value, err)
	}
	return policy
}

func TestParseDataPolicy(t *testing.T) {
	tests := []struct {
		value    string
		name     string
		duration time.Duration
	}{
		{"", "temporary", 0},
		{"temporary", "temporary", 0},
		{"permanent", "permanent", 0},
		{"once", "once", 0},
		{"30m", "30m", 30 * time.Minute},
		{"12h", "12h", 12 * time.Hour},
		{"7d", "7d", 7 * 24 * time.Hour},
		{"720h", "720h", 30 * 24 * time.Hour},
		{"43200m", "43200m", 30 * 24 * time.Hour},
		{"30d", "30d", 30 * 24 * time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			policy, err := ParseDataPolicy(tc.value)
			if err != nil {
				t.Fatalf("ParseDataPolicy failed: %v", err)
			}
			if policy.Name != tc.name {
				t.Fatalf("Name = %q, want %q", policy.Name, tc.name)
			}
			if policy.CustomDuration != tc.duration {
				t.Fatalf("CustomDuration = %v, want %v", policy.CustomDuration, tc.duration)
			}
		})
	}
}

func TestParseDataPolicyRejectsInvalidValues(t *testing.T) {
	tests := []string{"0m", "31d", "721h", "43201m", "1w", "1.5h", "m", "10", "-1h"}

	for _, value := range tests {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseDataPolicy(value); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("expected ErrInvalidPolicy, got %v", err)
			}
		})
	}
}

func TestStoreViewConsumesOnceOnSuccess(t *testing.T) {
	store := newTestStore(t)

	meta, _, _, _, err := store.Create(strings.NewReader("hello once"), "once.txt", "text/plain; charset=utf-8", false, mustParsePolicy(t, "once"), "once1")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	err = store.View(meta.ID, "", func(entry *Entry) error {
		data, readErr := io.ReadAll(entry.File)
		if readErr != nil {
			return readErr
		}
		if string(data) != "hello once" {
			t.Fatalf("unexpected content: %q", string(data))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View failed: %v", err)
	}

	entry, err := store.Open(meta.ID, "")
	if !errors.Is(err, ErrNotFound) {
		if entry != nil && entry.File != nil {
			_ = entry.File.Close()
		}
		t.Fatalf("expected ErrNotFound after successful once view, got entry=%v err=%v", entry != nil, err)
	}
}

func TestStoreViewAllowsConcurrentReads(t *testing.T) {
	store := newTestStore(t)
	meta, _, _, _, err := store.Create(strings.NewReader("shared"), "shared.txt", "text/plain", false, mustParsePolicy(t, "permanent"), "shared1")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errs <- store.View(meta.ID, "", func(entry *Entry) error {
				entered <- struct{}{}
				<-release
				_, readErr := io.ReadAll(entry.File)
				return readErr
			})
		}()
	}

	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(release)
			workers.Wait()
			t.Fatal("concurrent view was serialized")
		}
	}
	close(release)
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("View failed: %v", err)
		}
	}
}

func TestStoreViewStillSerializesOnceReads(t *testing.T) {
	store := newTestStore(t)
	meta, _, _, _, err := store.Create(strings.NewReader("once"), "once.txt", "text/plain", false, mustParsePolicy(t, "once"), "once4")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		results <- store.View(meta.ID, "", func(entry *Entry) error {
			close(firstEntered)
			<-releaseFirst
			_, readErr := io.ReadAll(entry.File)
			return readErr
		})
	}()
	<-firstEntered
	go func() {
		results <- store.View(meta.ID, "", func(entry *Entry) error {
			_, readErr := io.ReadAll(entry.File)
			return readErr
		})
	}()

	select {
	case err := <-results:
		t.Fatalf("once view completed before the active view released its lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)

	var successes int
	var notFound int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrNotFound):
			notFound++
		default:
			t.Fatalf("unexpected View error: %v", err)
		}
	}
	if successes != 1 || notFound != 1 {
		t.Fatalf("once results: successes=%d notFound=%d", successes, notFound)
	}
}

func TestStoreListCacheInvalidatesOnMutation(t *testing.T) {
	store := newTestStore(t)
	first, _, _, manageToken, err := store.Create(strings.NewReader("one"), "one.txt", "text/plain", false, mustParsePolicy(t, "temporary"), "cache1")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	items, err := store.ListPastes()
	if err != nil || len(items) != 1 {
		t.Fatalf("initial ListPastes = %d items, err=%v", len(items), err)
	}
	if _, _, _, _, err := store.Create(strings.NewReader("two"), "two.txt", "text/plain", false, mustParsePolicy(t, "temporary"), "cache2"); err != nil {
		t.Fatalf("second Create failed: %v", err)
	}
	items, err = store.ListPastes()
	if err != nil || len(items) != 2 {
		t.Fatalf("ListPastes after create = %d items, err=%v", len(items), err)
	}

	if _, err := store.SetLabel(first.ID, manageToken, "updated"); err != nil {
		t.Fatalf("SetLabel failed: %v", err)
	}
	items, err = store.ListPastes()
	if err != nil {
		t.Fatalf("ListPastes after label failed: %v", err)
	}
	found := false
	for _, item := range items {
		if item.ID == first.ID {
			found = true
			if item.Label != "updated" {
				t.Fatalf("cached label = %q, want updated", item.Label)
			}
		}
	}
	if !found {
		t.Fatalf("updated paste %q missing from cached list", first.ID)
	}
}

func TestStoreViewKeepsOnceOnCallbackFailure(t *testing.T) {
	store := newTestStore(t)

	meta, _, _, _, err := store.Create(strings.NewReader("hello once"), "once.txt", "text/plain; charset=utf-8", false, mustParsePolicy(t, "once"), "once2")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sentinel := errors.New("callback failed")
	err = store.View(meta.ID, "", func(entry *Entry) error {
		_, _ = io.ReadAll(entry.File)
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}

	entry, err := store.Open(meta.ID, "")
	if err != nil {
		t.Fatalf("Open failed after callback error: %v", err)
	}
	_ = entry.File.Close()
}

func TestStoreCloneDoesNotConsumeOnceSource(t *testing.T) {
	store := newTestStore(t)

	meta, _, _, _, err := store.Create(strings.NewReader("clone me"), "clone.txt", "text/plain; charset=utf-8", false, mustParsePolicy(t, "once"), "once3")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if _, _, _, _, err := store.Clone(meta.ID, "", false, mustParsePolicy(t, ""), "clone1"); err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	entry, err := store.Open(meta.ID, "")
	if err != nil {
		t.Fatalf("Open failed after clone: %v", err)
	}
	_ = entry.File.Close()
}

func TestStoreLabelLifecycle(t *testing.T) {
	store := newTestStore(t)
	meta, _, _, manageToken, err := store.CreateWithLabel(strings.NewReader("labeled"), "service.log", "text/plain", false, mustParsePolicy(t, "temporary"), "label1", "production log")
	if err != nil {
		t.Fatalf("CreateWithLabel failed: %v", err)
	}
	if meta.Label != "production log" {
		t.Fatalf("label = %q, want production log", meta.Label)
	}

	clone, _, _, _, err := store.Clone(meta.ID, "", false, mustParsePolicy(t, "temporary"), "label2")
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}
	if clone.Label != meta.Label {
		t.Fatalf("clone label = %q, want %q", clone.Label, meta.Label)
	}

	updated, err := store.SetLabel(meta.ID, manageToken, "deployment log")
	if err != nil {
		t.Fatalf("SetLabel failed: %v", err)
	}
	if updated.Label != "deployment log" {
		t.Fatalf("updated label = %q", updated.Label)
	}
	if _, err := store.SetLabel(meta.ID, manageToken, strings.Repeat("a", 101)); !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("SetLabel oversized error = %v, want ErrInvalidLabel", err)
	}
}

func TestStoreCreateCustomDataPolicy(t *testing.T) {
	store := newTestStore(t)

	before := time.Now().UTC()
	meta, _, _, _, err := store.Create(strings.NewReader("custom ttl"), "custom.txt", "text/plain; charset=utf-8", false, mustParsePolicy(t, "12h"), "custom12h")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	after := time.Now().UTC()

	if meta.DataPolicy != "12h" {
		t.Fatalf("DataPolicy = %q, want %q", meta.DataPolicy, "12h")
	}
	minExpires := before.Add(12 * time.Hour)
	maxExpires := after.Add(12 * time.Hour)
	if meta.ExpiresAt.Before(minExpires) || meta.ExpiresAt.After(maxExpires) {
		t.Fatalf("ExpiresAt = %v, want between %v and %v", meta.ExpiresAt, minExpires, maxExpires)
	}
}

func TestStoreSetDataPolicyCustomDuration(t *testing.T) {
	store := newTestStore(t)

	meta, _, _, manageToken, err := store.Create(strings.NewReader("manage me"), "manage.txt", "text/plain; charset=utf-8", false, mustParsePolicy(t, "temporary"), "manage12h")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	before := time.Now().UTC()
	updated, err := store.SetDataPolicy(meta.ID, manageToken, "12h")
	if err != nil {
		t.Fatalf("SetDataPolicy failed: %v", err)
	}
	after := time.Now().UTC()

	if updated.DataPolicy != "12h" {
		t.Fatalf("DataPolicy = %q, want %q", updated.DataPolicy, "12h")
	}

	minExpires := before.Add(12 * time.Hour)
	maxExpires := after.Add(12 * time.Hour)
	if updated.ExpiresAt.Before(minExpires) || updated.ExpiresAt.After(maxExpires) {
		t.Fatalf("ExpiresAt = %v, want between %v and %v", updated.ExpiresAt, minExpires, maxExpires)
	}
}

func TestStoreAdminUsername(t *testing.T) {
	store := newTestStore(t)

	if err := store.CreateAdmin("admin", "secret"); err != nil {
		t.Fatalf("CreateAdmin failed: %v", err)
	}

	username, err := store.AdminUsername()
	if err != nil {
		t.Fatalf("AdminUsername failed: %v", err)
	}
	if username != "admin" {
		t.Fatalf("AdminUsername = %q, want %q", username, "admin")
	}
}

func TestStoreMigrationMarkers(t *testing.T) {
	store := newTestStore(t)

	done, err := store.migrationDone("local_pastes_to_mysql")
	if err != nil {
		t.Fatalf("migrationDone before mark failed: %v", err)
	}
	if done {
		t.Fatalf("migrationDone before mark = %v, want false", done)
	}

	if err := store.markMigrationDone("local_pastes_to_mysql"); err != nil {
		t.Fatalf("markMigrationDone failed: %v", err)
	}

	done, err = store.migrationDone("local_pastes_to_mysql")
	if err != nil {
		t.Fatalf("migrationDone after mark failed: %v", err)
	}
	if !done {
		t.Fatalf("migrationDone after mark = %v, want true", done)
	}
}
