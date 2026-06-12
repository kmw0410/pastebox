package internal

import (
	"errors"
	"io"
	"strings"
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

func TestStoreViewConsumesOnceOnSuccess(t *testing.T) {
	store := newTestStore(t)

	meta, _, _, _, err := store.Create(strings.NewReader("hello once"), "once.txt", "text/plain; charset=utf-8", false, false, true, "once1")
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

func TestStoreViewKeepsOnceOnCallbackFailure(t *testing.T) {
	store := newTestStore(t)

	meta, _, _, _, err := store.Create(strings.NewReader("hello once"), "once.txt", "text/plain; charset=utf-8", false, false, true, "once2")
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

	meta, _, _, _, err := store.Create(strings.NewReader("clone me"), "clone.txt", "text/plain; charset=utf-8", false, false, true, "once3")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if _, _, _, _, err := store.Clone(meta.ID, "", false, false, false, "clone1"); err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	entry, err := store.Open(meta.ID, "")
	if err != nil {
		t.Fatalf("Open failed after clone: %v", err)
	}
	_ = entry.File.Close()
}
