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

func (s *Store) getMySQLAdminSetting(key string) (string, bool, error) {
	var value string

	err := s.mysqlDB.QueryRow(`
		SELECT value
		FROM pastebox_settings
		WHERE `+"`key`"+` = ?
	`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	return value, true, nil
}

func (s *Store) setMySQLAdminSetting(key string, value string) error {
	_, err := s.mysqlDB.Exec(`
		INSERT INTO pastebox_settings (
			`+"`key`"+`,
			value,
			updated_at_unix
		) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			value = VALUES(value),
			updated_at_unix = VALUES(updated_at_unix)
	`, key, value, time.Now().UTC().Unix())

	return err
}

func (s *Store) clearAdminSessions() error {
	db := s.adminSessionDB()
	_, err := db.Exec(`DELETE FROM admin_sessions`)
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
	if exists {
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
	}

	if err := s.migrateSQLiteAdminSessionsToMySQL(); err != nil {
		return err
	}

	if err := s.migrateSQLiteAdminSettingsToMySQL(); err != nil {
		return err
	}

	return s.markMigrationDone("sqlite_admin_accounts_to_mysql")
}

func (s *Store) migrateSQLiteAdminSessionsToMySQL() error {
	rows, err := s.adminDB.Query(`
		SELECT token_hash, created_at_unix, expires_at_unix
		FROM admin_sessions
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type sessionRow struct {
		tokenHash     string
		createdAtUnix int64
		expiresAtUnix int64
	}

	sessions := make([]sessionRow, 0)
	for rows.Next() {
		var item sessionRow
		if err := rows.Scan(&item.tokenHash, &item.createdAtUnix, &item.expiresAtUnix); err != nil {
			return err
		}
		sessions = append(sessions, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, item := range sessions {
		if _, err := s.mysqlDB.Exec(`
			INSERT INTO admin_sessions (
				token_hash,
				created_at_unix,
				expires_at_unix
			) VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE
				created_at_unix = VALUES(created_at_unix),
				expires_at_unix = VALUES(expires_at_unix)
		`, item.tokenHash, item.createdAtUnix, item.expiresAtUnix); err != nil {
			return err
		}
	}

	if len(sessions) == 0 {
		return nil
	}

	_, err = s.adminDB.Exec(`DELETE FROM admin_sessions`)
	return err
}

func (s *Store) migrateSQLiteAdminSettingsToMySQL() error {
	rows, err := s.adminDB.Query(`
		SELECT ` + "`key`" + `, value, updated_at_unix
		FROM pastebox_settings
		WHERE ` + "`key`" + ` NOT LIKE 'migration.%'
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type settingRow struct {
		key           string
		value         string
		updatedAtUnix int64
	}

	settings := make([]settingRow, 0)
	for rows.Next() {
		var item settingRow
		if err := rows.Scan(&item.key, &item.value, &item.updatedAtUnix); err != nil {
			return err
		}
		settings = append(settings, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, item := range settings {
		if _, err := s.mysqlDB.Exec(`
			INSERT INTO pastebox_settings (
				`+"`key`"+`,
				value,
				updated_at_unix
			) VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE
				value = VALUES(value),
				updated_at_unix = VALUES(updated_at_unix)
		`, item.key, item.value, item.updatedAtUnix); err != nil {
			return err
		}
	}

	if len(settings) == 0 {
		return nil
	}

	_, err = s.adminDB.Exec(`DELETE FROM pastebox_settings WHERE ` + "`key`" + ` NOT LIKE 'migration.%'`)
	return err
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
