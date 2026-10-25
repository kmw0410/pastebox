package internal

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// Each run owns a fresh database. The supplied account needs CREATE/DROP DATABASE.
func TestMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("PASTEBOX_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set PASTEBOX_TEST_MYSQL_DSN to run against MySQL or MariaDB")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test database DSN")
	}
	cfg.DBName = ""
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal("cannot open test database connection")
	}
	defer db.Close()
	name := "pastebox_test_" + time.Now().Format("20060102150405")
	suffix, err := randomString("abcdefghijklmnopqrstuvwxyz", 8)
	if err != nil {
		t.Fatal(err)
	}
	name += suffix
	if _, err := db.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create isolated database: %v", err)
	}
	defer func() {
		if _, err := db.Exec("DROP DATABASE " + name); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
	}()
	cfg.DBName = name
	cfg.ParseTime = true
	dir := t.TempDir()
	local, err := NewStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = local.Create(strings.NewReader("migrated"), "old.txt", "text/plain", false, mustParsePolicy(t, "permanent"), "old")
	if err != nil {
		t.Fatal(err)
	}
	if err := local.CreateAdmin("admin", "integration-password"); err != nil {
		t.Fatal(err)
	}
	if err := local.SetUploadsDisabled(true); err != nil {
		t.Fatal(err)
	}
	session, err := local.CreateAdminSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := local.adminDB.Close(); err != nil {
		t.Fatal(err)
	}
	opts := StoreOptions{DataDir: dir, TTL: time.Hour, StorageBackend: "mysql", MySQLDSN: cfg.FormatDSN(), MigrateLocalPastes: true, MigrateSQLiteAdminAccounts: true}
	store, err := NewStoreWithOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer store.adminDB.Close()
	defer store.mysqlDB.Close()
	read := func(id, password, want string) {
		t.Helper()
		err := store.View(id, password, func(entry *Entry) error {
			content, err := io.ReadAll(entry.File)
			if err == nil && string(content) != want {
				t.Errorf("content mismatch for %s", id)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	read("old", "", "migrated")
	if ok, err := store.AuthenticateAdmin("admin", "integration-password"); err != nil || !ok {
		t.Fatalf("admin migration: %v", err)
	}
	if ok, err := store.ValidAdminSession(session); err != nil || !ok {
		t.Fatalf("session migration: %v", err)
	}
	if disabled, err := store.UploadsDisabled(); err != nil || !disabled {
		t.Fatalf("settings migration: %v", err)
	}
	payload := make([]byte, 2*mysqlChunkSize)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	content := hex.EncodeToString(payload)
	meta, _, _, token, err := store.CreateWithPasswordAndLabel(strings.NewReader(content), "large.log", "text/plain", false, "custom-password", mustParsePolicy(t, "permanent"), "large", "label")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Size != int64(len(content)) || meta.Filename != "large.log" {
		t.Fatal("metadata mismatch")
	}
	var storedChunks int
	if err := store.mysqlDB.QueryRow("SELECT COUNT(*) FROM paste_content_chunks WHERE paste_id = ?", meta.ID).Scan(&storedChunks); err != nil || storedChunks < 2 {
		t.Fatalf("multi-chunk fixture: %d chunks, %v", storedChunks, err)
	}
	read(meta.ID, "custom-password", content)
	if _, err := store.Open(meta.ID, "wrong"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("password enforcement: %v", err)
	}
	if _, _, _, _, err := store.Create(strings.NewReader("duplicate"), "", "text/plain", false, mustParsePolicy(t, "temporary"), meta.ID); !errors.Is(err, ErrCodeExists) {
		t.Fatalf("duplicate code: %v", err)
	}
	if _, err := store.SetLabel(meta.ID, token, "updated"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetDataPolicy(meta.ID, token, "1h"); err != nil {
		t.Fatal(err)
	}
	clone, _, _, _, err := store.Clone(meta.ID, "custom-password", false, mustParsePolicy(t, "once"), "copy")
	if err != nil {
		t.Fatal(err)
	}
	if clone.Filename != meta.Filename {
		t.Fatal("clone lost filename")
	}
	read(clone.ID, "", content)
	if _, err := store.Open(clone.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("once not consumed: %v", err)
	}
	if err := store.DeleteManaged(meta.ID, "wrong"); !errors.Is(err, ErrInvalidManageToken) {
		t.Fatalf("delete authorization: %v", err)
	}
	if err := store.DeleteManaged(meta.ID, token); err != nil {
		t.Fatal(err)
	}
	var chunks int
	if err := store.mysqlDB.QueryRow("SELECT COUNT(*) FROM paste_content_chunks WHERE paste_id = ?", meta.ID).Scan(&chunks); err != nil || chunks != 0 {
		t.Fatalf("orphaned chunks: %d, %v", chunks, err)
	}
	// Reopening with migration flags must not recreate deleted migrated data.
	if err := store.AdminDelete("old"); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStoreWithOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.adminDB.Close()
	defer reopened.mysqlDB.Close()
	if _, err := reopened.Open("old", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("migration repeated: %v", err)
	}
}
