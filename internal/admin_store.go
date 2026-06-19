package internal

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

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
	if s.StorageBackend == "mysql" {
		exists, err := s.adminExistsMySQL()
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}

	return s.adminExistsSQLite()
}

func (s *Store) adminExistsSQLite() (bool, error) {
	var count int

	err := s.adminDB.QueryRow(`SELECT COUNT(*) FROM pastebox_admin`).Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (s *Store) AdminUsername() (string, error) {
	if s.StorageBackend == "mysql" {
		username, err := s.adminUsernameMySQL()
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if username != "" {
			return username, nil
		}
	}

	return s.adminUsernameSQLite()
}

func (s *Store) adminUsernameSQLite() (string, error) {
	var username string

	err := s.adminDB.QueryRow(`
		SELECT username
		FROM pastebox_admin
		WHERE id = 1
	`).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("admin account not found")
	}
	if err != nil {
		return "", err
	}

	return username, nil
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

	if s.StorageBackend == "mysql" {
		return s.createAdminMySQL(username, password)
	}

	return s.createAdminSQLite(username, password)
}

func (s *Store) createAdminSQLite(username string, password string) error {
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

	if s.StorageBackend == "mysql" {
		if err := s.forceResetAdminPasswordMySQL(password); err == nil {
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}

	return s.forceResetAdminPasswordSQLite(password)
}

func (s *Store) forceResetAdminPasswordSQLite(password string) error {
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

	if s.StorageBackend == "mysql" {
		ok, err := s.authenticateAdminMySQL(username, password)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}

	return s.authenticateAdminSQLite(username, password)
}

func (s *Store) authenticateAdminSQLite(username string, password string) (bool, error) {
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

	if secureCompare(candidate, storedHash) {
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
	value, ok, err := s.getAdminSetting("uploads_disabled")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	return value == "true", nil
}

func (s *Store) SetUploadsDisabled(disabled bool) error {
	value := "false"
	if disabled {
		value = "true"
	}

	return s.setAdminSetting("uploads_disabled", value)
}

func (s *Store) getAdminSetting(key string) (string, bool, error) {
	var value string

	err := s.adminDB.QueryRow(`
		SELECT value
		FROM pastebox_settings
		WHERE key = ?
	`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	return value, true, nil
}

func (s *Store) setAdminSetting(key string, value string) error {
	_, err := s.adminDB.Exec(`
		INSERT INTO pastebox_settings (
			key,
			value,
			updated_at_unix
		) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at_unix = excluded.updated_at_unix
	`, key, value, time.Now().UTC().Unix())

	return err
}

func (s *Store) migrationDone(key string) (bool, error) {
	value, ok, err := s.getAdminSetting("migration." + key)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	return value == "true", nil
}

func (s *Store) markMigrationDone(key string) error {
	return s.setAdminSetting("migration."+key, "true")
}
