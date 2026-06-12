package internal

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound           = errors.New("not found")
	ErrInvalidPassword    = errors.New("invalid password")
	ErrInvalidManageToken = errors.New("invalid manage token")
	ErrInvalidDeleteToken = errors.New("invalid delete token")
	ErrAlreadyProtected   = errors.New("paste is already password protected")
	ErrInvalidPolicy      = errors.New("invalid policy")
	ErrInvalidCode        = errors.New("invalid code")
	ErrCodeExists         = errors.New("code already exists")
)

type Store struct {
	DataDir string
	TTL     time.Duration
	locks   *lockManager
	adminDB *sql.DB
}

type Metadata struct {
	ID              string    `json:"id"`
	Filename        string    `json:"filename,omitempty"`
	PasswordHash    string    `json:"password_hash,omitempty"`
	ManageTokenHash string    `json:"manage_token_hash,omitempty"`
	DeleteTokenHash string    `json:"delete_token_hash,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
	DataPolicy      string    `json:"data_policy,omitempty"`
	Size            int64     `json:"size"`
	ContentType     string    `json:"content_type"`
}

type Entry struct {
	Meta Metadata
	File *os.File
}

type AdminPasteItem struct {
	ID          string
	Filename    string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	DataPolicy  string
	Size        int64
	ContentType string
	Protected   bool
}

func NewStore(dataDir string, ttl time.Duration) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}

	adminDB, err := openAdminDB(filepath.Join(dataDir, "pastebox.db"))
	if err != nil {
		return nil, err
	}

	return &Store{
		DataDir: dataDir,
		TTL:     ttl,
		locks:   newLockManager(),
		adminDB: adminDB,
	}, nil
}

func (s *Store) Create(r io.Reader, filename string, contentType string, usePassword bool, permanent bool, once bool, customCode string) (Metadata, string, string, string, error) {
	return s.createFromReader(r, filename, contentType, usePassword, permanent, once, customCode)
}

func (s *Store) Clone(id string, password string, usePassword bool, permanent bool, once bool, customCode string) (Metadata, string, string, string, error) {
	entry, err := s.Open(id, password)
	if err != nil {
		return Metadata{}, "", "", "", err
	}
	defer entry.File.Close()

	return s.createFromReader(entry.File, entry.Meta.Filename, entry.Meta.ContentType, usePassword, permanent, once, customCode)
}

func (s *Store) createFromReader(r io.Reader, filename string, contentType string, usePassword bool, permanent bool, once bool, customCode string) (Metadata, string, string, string, error) {
	id, path, err := s.reservePath(customCode)
	if err != nil {
		return Metadata{}, "", "", "", err
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return Metadata{}, "", "", "", ErrCodeExists
		}

		return Metadata{}, "", "", "", err
	}

	size, copyErr := io.Copy(file, r)
	closeErr := file.Close()

	if copyErr != nil {
		_ = os.Remove(path)
		return Metadata{}, "", "", "", copyErr
	}

	if closeErr != nil {
		_ = os.Remove(path)
		return Metadata{}, "", "", "", closeErr
	}

	password, passwordHash, err := maybeCreatePassword(usePassword)
	if err != nil {
		_ = os.Remove(path)
		return Metadata{}, "", "", "", err
	}

	manageToken, err := randomString(tokenAlphabet, 32)
	if err != nil {
		_ = os.Remove(path)
		return Metadata{}, "", "", "", err
	}

	deleteToken, err := randomString(tokenAlphabet, 32)
	if err != nil {
		_ = os.Remove(path)
		return Metadata{}, "", "", "", err
	}

	now := time.Now().UTC()

	dataPolicy := "temporary"
	expiresAt := now.Add(s.TTL)

	if permanent {
		dataPolicy = "permanent"
		expiresAt = time.Time{}
	} else if once {
		dataPolicy = "once"
	}

	meta := Metadata{
		ID:              id,
		Filename:        strings.TrimSpace(filename),
		PasswordHash:    passwordHash,
		ManageTokenHash: hashSecret(manageToken),
		DeleteTokenHash: hashSecret(deleteToken),
		CreatedAt:       now,
		ExpiresAt:       expiresAt,
		DataPolicy:      dataPolicy,
		Size:            size,
		ContentType:     contentType,
	}

	if err := s.writeMetadata(meta); err != nil {
		_ = os.Remove(path)
		return Metadata{}, "", "", "", err
	}

	return meta, password, deleteToken, manageToken, nil
}

func (s *Store) Open(id string, password string) (*Entry, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	path := s.path(id)

	meta, err := s.readMetadata(id)
	if err != nil {
		return nil, ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		_ = os.Remove(path)
		_ = os.Remove(metaPath(path))
		return nil, ErrNotFound
	}

	if err := checkPassword(meta, password); err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, ErrNotFound
	}

	return &Entry{
		Meta: meta,
		File: file,
	}, nil
}

func (s *Store) View(id string, password string, fn func(*Entry) error) error {
	if !validID(id) {
		return ErrNotFound
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	path := s.path(id)

	meta, err := s.readMetadata(id)
	if err != nil {
		return ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		_ = os.Remove(path)
		_ = os.Remove(metaPath(path))
		return ErrNotFound
	}

	if err := checkPassword(meta, password); err != nil {
		return err
	}

	file, err := os.Open(path)
	if err != nil {
		return ErrNotFound
	}

	entry := &Entry{
		Meta: meta,
		File: file,
	}

	callbackErr := fn(entry)
	closeErr := file.Close()

	if callbackErr != nil {
		return callbackErr
	}

	if closeErr != nil {
		return closeErr
	}

	if strings.EqualFold(meta.DataPolicy, "once") {
		fileErr := os.Remove(path)
		metaErr := os.Remove(metaPath(path))

		if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
			return fileErr
		}

		if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
			return metaErr
		}
	}

	return nil
}

func (s *Store) Delete(id string, token string) error {
	if !validID(id) {
		return ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidDeleteToken
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	path := s.path(id)

	meta, err := s.readMetadata(id)
	if err != nil {
		return ErrNotFound
	}

	if err := checkDeleteToken(meta, token); err != nil {
		return err
	}

	fileErr := os.Remove(path)
	metaErr := os.Remove(metaPath(path))

	if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
		return fileErr
	}

	if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
		return metaErr
	}

	return nil
}

func (s *Store) DeleteManaged(id string, token string) error {
	if !validID(id) {
		return ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidManageToken
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	path := s.path(id)

	meta, err := s.readMetadata(id)
	if err != nil {
		return ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		_ = os.Remove(path)
		_ = os.Remove(metaPath(path))
		return ErrNotFound
	}

	if err := checkManageToken(meta, token); err != nil {
		return err
	}

	fileErr := os.Remove(path)
	metaErr := os.Remove(metaPath(path))

	if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
		return fileErr
	}

	if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
		return metaErr
	}

	return nil
}

func (s *Store) AdminDelete(id string) error {
	if !validID(id) {
		return ErrNotFound
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	path := s.path(id)

	fileErr := os.Remove(path)
	metaErr := os.Remove(metaPath(path))

	if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
		return fileErr
	}

	if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
		return metaErr
	}

	return nil
}

func (s *Store) AdminDeleteAll() (int, error) {
	entries, err := os.ReadDir(s.DataDir)
	if err != nil {
		return 0, err
	}

	deleted := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}

		id := strings.TrimSuffix(name, ".json")
		if !validID(id) {
			continue
		}

		unlock := s.locks.Lock(id)
		path := s.path(id)

		fileErr := os.Remove(path)
		metaErr := os.Remove(metaPath(path))
		unlock()

		if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
			return deleted, fileErr
		}

		if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
			return deleted, metaErr
		}

		deleted++
	}

	return deleted, nil
}

func (s *Store) CleanupExpired() error {
	entries, err := os.ReadDir(s.DataDir)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		if strings.HasSuffix(name, ".json") {
			continue
		}

		if !validID(name) {
			continue
		}

		unlock := s.locks.Lock(name)

		meta, err := s.readMetadata(name)
		if err == nil && isExpired(meta, now) {
			path := s.path(name)
			_ = os.Remove(path)
			_ = os.Remove(metaPath(path))
		}

		unlock()
	}

	return nil
}

func (s *Store) ListPastes() ([]AdminPasteItem, error) {
	entries, err := os.ReadDir(s.DataDir)
	if err != nil {
		return nil, err
	}

	items := make([]AdminPasteItem, 0)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}

		id := strings.TrimSuffix(name, ".json")
		if !validID(id) {
			continue
		}

		unlock := s.locks.Lock(id)
		meta, err := s.readMetadata(id)
		unlock()

		if err != nil {
			continue
		}

		items = append(items, AdminPasteItem{
			ID:          meta.ID,
			Filename:    meta.Filename,
			CreatedAt:   meta.CreatedAt,
			ExpiresAt:   meta.ExpiresAt,
			DataPolicy:  meta.DataPolicy,
			Size:        meta.Size,
			ContentType: meta.ContentType,
			Protected:   meta.PasswordHash != "",
		})
	}

	return items, nil
}

func (s *Store) ManageMetadata(id string, token string) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return Metadata{}, ErrInvalidManageToken
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	meta, err := s.readMetadata(id)
	if err != nil {
		return Metadata{}, ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		path := s.path(id)
		_ = os.Remove(path)
		_ = os.Remove(metaPath(path))
		return Metadata{}, ErrNotFound
	}

	if err := checkManageToken(meta, token); err != nil {
		return Metadata{}, err
	}

	return meta, nil
}

func (s *Store) SetPasswordProtection(id string, token string) (Metadata, string, error) {
	if !validID(id) {
		return Metadata{}, "", ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return Metadata{}, "", ErrInvalidManageToken
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	path := s.path(id)

	meta, err := s.readMetadata(id)
	if err != nil {
		return Metadata{}, "", ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		_ = os.Remove(path)
		_ = os.Remove(metaPath(path))
		return Metadata{}, "", ErrNotFound
	}

	if err := checkManageToken(meta, token); err != nil {
		return Metadata{}, "", err
	}

	if meta.PasswordHash != "" {
		return Metadata{}, "", ErrAlreadyProtected
	}

	password, passwordHash, err := maybeCreatePassword(true)
	if err != nil {
		return Metadata{}, "", err
	}

	meta.PasswordHash = passwordHash

	if err := s.writeMetadata(meta); err != nil {
		return Metadata{}, "", err
	}

	return meta, password, nil
}

func (s *Store) ClearPasswordProtection(id string, token string, password string) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return Metadata{}, ErrInvalidManageToken
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	path := s.path(id)

	meta, err := s.readMetadata(id)
	if err != nil {
		return Metadata{}, ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		_ = os.Remove(path)
		_ = os.Remove(metaPath(path))
		return Metadata{}, ErrNotFound
	}

	if err := checkManageToken(meta, token); err != nil {
		return Metadata{}, err
	}

	if err := checkPassword(meta, password); err != nil {
		return Metadata{}, err
	}

	meta.PasswordHash = ""

	if err := s.writeMetadata(meta); err != nil {
		return Metadata{}, err
	}

	return meta, nil
}

func (s *Store) SetDataPolicy(id string, token string, policy string) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return Metadata{}, ErrInvalidManageToken
	}

	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy != "temporary" && policy != "permanent" && policy != "once" {
		return Metadata{}, ErrInvalidPolicy
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	path := s.path(id)
	now := time.Now().UTC()

	meta, err := s.readMetadata(id)
	if err != nil {
		return Metadata{}, ErrNotFound
	}

	if isExpired(meta, now) {
		_ = os.Remove(path)
		_ = os.Remove(metaPath(path))
		return Metadata{}, ErrNotFound
	}

	if err := checkManageToken(meta, token); err != nil {
		return Metadata{}, err
	}

	meta.DataPolicy = policy
	switch policy {
	case "permanent":
		meta.ExpiresAt = time.Time{}
	case "temporary", "once":
		meta.ExpiresAt = now.Add(s.TTL)
	}

	if err := s.writeMetadata(meta); err != nil {
		return Metadata{}, err
	}

	return meta, nil
}

func (s *Store) reservePath(customCode string) (string, string, error) {
	customCode = strings.TrimSpace(customCode)

	if customCode != "" {
		if !validID(customCode) {
			return "", "", ErrInvalidCode
		}

		path := s.path(customCode)

		if _, err := os.Stat(path); err == nil {
			return "", "", ErrCodeExists
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}

		if _, err := os.Stat(metaPath(path)); err == nil {
			return "", "", ErrCodeExists
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}

		return customCode, path, nil
	}

	for i := 0; i < 100; i++ {
		id, err := randomString(idAlphabet, 5)
		if err != nil {
			return "", "", err
		}

		path := s.path(id)

		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return id, path, nil
		}
	}

	return "", "", errors.New("failed to reserve random id")
}

func (s *Store) path(id string) string {
	return filepath.Join(s.DataDir, id)
}

func (s *Store) writeMetadata(meta Metadata) error {
	path := s.path(meta.ID)
	metaFile := metaPath(path)

	tmp := metaFile + ".tmp"

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tmp, metaFile)
}

func (s *Store) readMetadata(id string) (Metadata, error) {
	var meta Metadata

	data, err := os.ReadFile(metaPath(s.path(id)))
	if err != nil {
		return meta, err
	}

	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}

	return meta, nil
}

func openAdminDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		_ = db.Close()
		return nil, err
	}

	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS pastebox_admin (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			salt TEXT NOT NULL,
			created_at_unix INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS admin_sessions (
			token_hash TEXT PRIMARY KEY,
			created_at_unix INTEGER NOT NULL,
			expires_at_unix INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS pastebox_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at_unix INTEGER NOT NULL
		);
	`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func (s *Store) AdminExists() (bool, error) {
	var count int

	err := s.adminDB.QueryRow(`SELECT COUNT(*) FROM pastebox_admin`).Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (s *Store) CreateAdmin(username string, password string) error {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)

	if username == "" || password == "" {
		return errors.New("username and password required")
	}

	exists, err := s.AdminExists()
	if err != nil {
		return err
	}

	if exists {
		return errors.New("admin account already exists")
	}

	salt, err := randomString(tokenAlphabet, 32)
	if err != nil {
		return err
	}

	hash := hashAdminPassword(password, salt)

	_, err = s.adminDB.Exec(`
		INSERT INTO pastebox_admin (
			id,
			username,
			password_hash,
			salt,
			created_at_unix
		) VALUES (1, ?, ?, ?, ?)
	`, username, hash, salt, time.Now().UTC().Unix())

	return err
}

func (s *Store) ForceResetAdminPassword(password string) error {
	password = strings.TrimSpace(password)
	if password == "" {
		return errors.New("password required")
	}

	exists, err := s.AdminExists()
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("admin account not found")
	}

	salt, err := randomString(tokenAlphabet, 32)
	if err != nil {
		return err
	}

	hash := hashAdminPassword(password, salt)

	tx, err := s.adminDB.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.Exec(`
		UPDATE pastebox_admin
		SET password_hash = ?, salt = ?
		WHERE id = 1
	`, hash, salt)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("admin account not found")
	}

	if _, err := tx.Exec(`DELETE FROM admin_sessions`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil

	return nil
}

func (s *Store) AuthenticateAdmin(username string, password string) (bool, error) {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)

	var storedHash string
	var salt string

	err := s.adminDB.QueryRow(`
		SELECT password_hash, salt
		FROM pastebox_admin
		WHERE username = ?
	`, username).Scan(&storedHash, &salt)

	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	candidate := hashAdminPassword(password, salt)

	if subtle.ConstantTimeCompare([]byte(candidate), []byte(storedHash)) == 1 {
		return true, nil
	}

	return false, nil
}

func (s *Store) CreateAdminSession() (string, error) {
	token, err := randomString(tokenAlphabet, 48)
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()
	expires := now.Add(24 * time.Hour)

	_, err = s.adminDB.Exec(`
		INSERT INTO admin_sessions (
			token_hash,
			created_at_unix,
			expires_at_unix
		) VALUES (?, ?, ?)
	`, hashSecret(token), now.Unix(), expires.Unix())

	if err != nil {
		return "", err
	}

	return token, nil
}

func (s *Store) ValidAdminSession(token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}

	now := time.Now().UTC().Unix()

	var count int
	err := s.adminDB.QueryRow(`
		SELECT COUNT(*)
		FROM admin_sessions
		WHERE token_hash = ?
		  AND expires_at_unix > ?
	`, hashSecret(token), now).Scan(&count)

	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (s *Store) DeleteAdminSession(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}

	_, err := s.adminDB.Exec(`
		DELETE FROM admin_sessions
		WHERE token_hash = ?
	`, hashSecret(token))

	return err
}

func (s *Store) UploadsDisabled() (bool, error) {
	var value string

	err := s.adminDB.QueryRow(`
		SELECT value
		FROM pastebox_settings
		WHERE key = 'uploads_disabled'
	`).Scan(&value)

	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return value == "true", nil
}

func (s *Store) SetUploadsDisabled(disabled bool) error {
	value := "false"
	if disabled {
		value = "true"
	}

	_, err := s.adminDB.Exec(`
		INSERT INTO pastebox_settings (
			key,
			value,
			updated_at_unix
		) VALUES ('uploads_disabled', ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at_unix = excluded.updated_at_unix
	`, value, time.Now().UTC().Unix())

	return err
}

func maybeCreatePassword(usePassword bool) (string, string, error) {
	if !usePassword {
		return "", "", nil
	}

	password, err := generatePassword(8)
	if err != nil {
		return "", "", err
	}

	return password, hashSecret(password), nil
}

func checkPassword(meta Metadata, password string) error {
	if meta.PasswordHash == "" {
		return nil
	}

	if strings.TrimSpace(password) == "" || hashSecret(password) != meta.PasswordHash {
		return ErrInvalidPassword
	}

	return nil
}

func checkDeleteToken(meta Metadata, token string) error {
	if meta.DeleteTokenHash == "" || hashSecret(token) != meta.DeleteTokenHash {
		return ErrInvalidDeleteToken
	}

	return nil
}

func checkManageToken(meta Metadata, token string) error {
	if meta.ManageTokenHash == "" || hashSecret(token) != meta.ManageTokenHash {
		return ErrInvalidManageToken
	}

	return nil
}

func isExpired(meta Metadata, now time.Time) bool {
	if strings.EqualFold(meta.DataPolicy, "permanent") {
		return false
	}

	if meta.ExpiresAt.IsZero() {
		return false
	}

	return now.After(meta.ExpiresAt)
}

func validID(id string) bool {
	if len(id) < 1 || len(id) > 10 {
		return false
	}

	for _, r := range id {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '_' || r == '-' {
			continue
		}
		return false
	}

	return true
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func hashAdminPassword(password string, salt string) string {
	data := []byte(salt + ":" + password)

	sum := sha256.Sum256(data)
	for i := 0; i < 200000; i++ {
		next := sha256.Sum256(sum[:])
		sum = next
	}

	return base64.RawStdEncoding.EncodeToString(sum[:])
}

func generatePassword(length int) (string, error) {
	if length < 4 {
		length = 8
	}

	upper := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lower := "abcdefghijklmnopqrstuvwxyz"
	digits := "0123456789"
	special := "!@#$%^&*_-+?{}[]"
	all := upper + lower + digits + special

	result := make([]byte, 0, length)

	a, err := randomChar(upper)
	if err != nil {
		return "", err
	}
	result = append(result, a)

	a, err = randomChar(lower)
	if err != nil {
		return "", err
	}
	result = append(result, a)

	a, err = randomChar(digits)
	if err != nil {
		return "", err
	}
	result = append(result, a)

	a, err = randomChar(special)
	if err != nil {
		return "", err
	}
	result = append(result, a)

	for len(result) < length {
		a, err = randomChar(all)
		if err != nil {
			return "", err
		}
		result = append(result, a)
	}

	if err := shuffleBytes(result); err != nil {
		return "", err
	}

	return string(result), nil
}

func randomChar(alphabet string) (byte, error) {
	n, err := randomIndex(len(alphabet))
	if err != nil {
		return 0, err
	}

	return alphabet[n], nil
}

func randomString(alphabet string, length int) (string, error) {
	result := make([]byte, length)

	for i := range result {
		ch, err := randomChar(alphabet)
		if err != nil {
			return "", err
		}
		result[i] = ch
	}

	return string(result), nil
}

func randomIndex(max int) (int, error) {
	if max <= 0 {
		return 0, errors.New("invalid max")
	}

	var b [1]byte

	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}

		limit := 256 - (256 % max)
		if int(b[0]) < limit {
			return int(b[0]) % max, nil
		}
	}
}

func shuffleBytes(data []byte) error {
	for i := len(data) - 1; i > 0; i-- {
		j, err := randomIndex(i + 1)
		if err != nil {
			return err
		}

		data[i], data[j] = data[j], data[i]
	}

	return nil
}

type lockManager struct {
	mu    sync.Mutex
	locks map[string]*refLock
}

type refLock struct {
	mu   sync.Mutex
	refs int
}

func newLockManager() *lockManager {
	return &lockManager{
		locks: make(map[string]*refLock),
	}
}

func (lm *lockManager) Lock(key string) func() {
	lm.mu.Lock()

	lock := lm.locks[key]
	if lock == nil {
		lock = &refLock{}
		lm.locks[key] = lock
	}

	lock.refs++
	lm.mu.Unlock()

	lock.mu.Lock()

	return func() {
		lock.mu.Unlock()

		lm.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(lm.locks, key)
		}
		lm.mu.Unlock()
	}
}

const idAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const tokenAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
