package internal

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"
)

func (s *Store) adminExistsMySQL() (bool, error) {
	var count int

	err := s.mysqlDB.QueryRow(`SELECT COUNT(*) FROM pastebox_admin`).Scan(&count)
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (s *Store) adminUsernameMySQL() (string, error) {
	var username string

	err := s.mysqlDB.QueryRow(`
		SELECT username
		FROM pastebox_admin
		WHERE id = 1
	`).Scan(&username)
	if err != nil {
		return "", err
	}

	return username, nil
}

func (s *Store) createAdminMySQL(username string, password string) error {
	salt, err := randomString(tokenAlphabet, 32)
	if err != nil {
		return err
	}

	hash := hashAdminPassword(password, salt)

	_, err = s.mysqlDB.Exec(`
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

func (s *Store) forceResetAdminPasswordMySQL(password string) error {
	salt, err := randomString(tokenAlphabet, 32)
	if err != nil {
		return err
	}

	hash := hashAdminPassword(password, salt)

	result, err := s.mysqlDB.Exec(`
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
		return sql.ErrNoRows
	}

	return s.clearAdminSessions()
}

func (s *Store) authenticateAdminMySQL(username string, password string) (bool, error) {
	var storedHash string
	var salt string

	err := s.mysqlDB.QueryRow(`
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
	return secureCompare(candidate, storedHash), nil
}

func (s *Store) clearAdminSessions() error {
	_, err := s.adminDB.Exec(`DELETE FROM admin_sessions`)
	return err
}

func (s *Store) migrateSQLiteAdminAccountsToMySQL() error {
	done, err := s.migrationDone("sqlite_admin_accounts_to_mysql")
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	exists, err := s.adminExistsSQLite()
	if err != nil {
		return err
	}
	if !exists {
		return s.markMigrationDone("sqlite_admin_accounts_to_mysql")
	}

	var username string
	var passwordHash string
	var salt string
	var createdAtUnix int64

	err = s.adminDB.QueryRow(`
		SELECT username, password_hash, salt, created_at_unix
		FROM pastebox_admin
		WHERE id = 1
	`).Scan(&username, &passwordHash, &salt, &createdAtUnix)
	if err != nil {
		return err
	}

	if _, err := s.mysqlDB.Exec(`
		INSERT INTO pastebox_admin (
			id,
			username,
			password_hash,
			salt,
			created_at_unix
		) VALUES (1, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			username = VALUES(username),
			password_hash = VALUES(password_hash),
			salt = VALUES(salt),
			created_at_unix = VALUES(created_at_unix)
	`, username, passwordHash, salt, createdAtUnix); err != nil {
		return err
	}

	if _, err := s.adminDB.Exec(`DELETE FROM pastebox_admin WHERE id = 1`); err != nil {
		return err
	}

	return s.markMigrationDone("sqlite_admin_accounts_to_mysql")
}

func (s *Store) migrateLocalPastesToMySQL() error {
	done, err := s.migrationDone("local_pastes_to_mysql")
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	entries, err := s.scanLocalPasteEntries()
	if err != nil {
		return err
	}

	for _, paste := range entries {
		file, err := os.Open(s.path(paste.id))
		if err != nil {
			return err
		}

		meta := paste.meta
		_, createErr := s.createMySQLWithID(paste.id, meta, file)
		closeErr := file.Close()
		if createErr != nil && !errors.Is(createErr, ErrCodeExists) {
			return createErr
		}
		if closeErr != nil {
			return closeErr
		}

		if err := s.removeLocalPaste(paste.id); err != nil {
			return err
		}
	}

	return s.markMigrationDone("local_pastes_to_mysql")
}

type localPasteEntry struct {
	id   string
	meta Metadata
}

func (s *Store) scanLocalPasteEntries() ([]localPasteEntry, error) {
	entries, err := os.ReadDir(s.DataDir)
	if err != nil {
		return nil, err
	}

	items := make([]localPasteEntry, 0)
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

		meta, err := s.readMetadata(name)
		if err != nil {
			return nil, err
		}
		if meta.ID == "" {
			meta.ID = name
		}

		items = append(items, localPasteEntry{id: name, meta: meta})
	}

	return items, nil
}

func (s *Store) removeLocalPaste(id string) error {
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
