package internal

import (
	"database/sql"
	"errors"
	"io"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/klauspost/compress/zstd"
)

const mysqlChunkSize = 1 << 20

func openMySQLPasteDB(dsn string) (*sql.DB, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, errors.New("MYSQL_DSN is required when STORAGE_BACKEND=mysql")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS pastebox_admin (
			id TINYINT NOT NULL PRIMARY KEY,
			username VARCHAR(255) NOT NULL UNIQUE,
			password_hash VARCHAR(255) NOT NULL,
			salt VARCHAR(255) NOT NULL,
			created_at_unix BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
	`); err != nil {
		_ = db.Close()
		return nil, err
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS admin_sessions (
			token_hash VARCHAR(128) PRIMARY KEY,
			created_at_unix BIGINT NOT NULL,
			expires_at_unix BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
	`); err != nil {
		_ = db.Close()
		return nil, err
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS pastebox_settings (
			` + "`key`" + ` VARCHAR(255) PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at_unix BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
	`); err != nil {
		_ = db.Close()
		return nil, err
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS paste_metadata (
			id VARCHAR(10) PRIMARY KEY,
			filename TEXT NULL,
			password_hash VARCHAR(128) NULL,
			manage_token_hash VARCHAR(128) NOT NULL,
			delete_token_hash VARCHAR(128) NOT NULL,
			created_at_unix BIGINT NOT NULL,
			expires_at_unix BIGINT NULL,
			data_policy VARCHAR(20) NOT NULL,
			size BIGINT NOT NULL,
			compressed_size BIGINT NOT NULL,
			content_type TEXT NULL,
			INDEX idx_expires_at_unix (expires_at_unix),
			INDEX idx_created_at_unix (created_at_unix)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
	`); err != nil {
		_ = db.Close()
		return nil, err
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS paste_content_chunks (
			paste_id VARCHAR(10) NOT NULL,
			chunk_index INT NOT NULL,
			data MEDIUMBLOB NOT NULL,
			PRIMARY KEY (paste_id, chunk_index),
			CONSTRAINT fk_paste_content_chunks_metadata
				FOREIGN KEY (paste_id) REFERENCES paste_metadata(id)
				ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
	`); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func (s *Store) createMySQLFromReader(r io.Reader, filename string, contentType string, usePassword bool, permanent bool, once bool, customCode string) (Metadata, string, string, string, error) {
	customCode = strings.TrimSpace(customCode)
	if customCode != "" && !validID(customCode) {
		return Metadata{}, "", "", "", ErrInvalidCode
	}

	password, passwordHash, err := maybeCreatePassword(usePassword)
	if err != nil {
		return Metadata{}, "", "", "", err
	}

	manageToken, err := randomString(tokenAlphabet, 32)
	if err != nil {
		return Metadata{}, "", "", "", err
	}

	deleteToken, err := randomString(tokenAlphabet, 32)
	if err != nil {
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

	baseMeta := Metadata{
		Filename:        strings.TrimSpace(filename),
		PasswordHash:    passwordHash,
		ManageTokenHash: hashSecret(manageToken),
		DeleteTokenHash: hashSecret(deleteToken),
		CreatedAt:       now,
		ExpiresAt:       expiresAt,
		DataPolicy:      dataPolicy,
		ContentType:     contentType,
	}

	if customCode != "" {
		meta, err := s.createMySQLWithID(customCode, baseMeta, r)
		if errors.Is(err, ErrCodeExists) {
			return Metadata{}, "", "", "", ErrCodeExists
		}
		return meta, password, deleteToken, manageToken, err
	}

	for i := 0; i < 100; i++ {
		id, err := randomString(idAlphabet, 5)
		if err != nil {
			return Metadata{}, "", "", "", err
		}

		meta, err := s.createMySQLWithID(id, baseMeta, r)
		if errors.Is(err, ErrCodeExists) {
			continue
		}
		return meta, password, deleteToken, manageToken, err
	}

	return Metadata{}, "", "", "", errors.New("failed to reserve random id")
}

func (s *Store) createMySQLWithID(id string, meta Metadata, r io.Reader) (Metadata, error) {
	unlock := s.locks.Lock(id)
	defer unlock()

	tx, err := s.mysqlDB.Begin()
	if err != nil {
		return Metadata{}, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	meta.ID = id
	expiresAt := mysqlNullUnix(meta.ExpiresAt)

	_, err = tx.Exec(`
		INSERT INTO paste_metadata (
			id,
			filename,
			password_hash,
			manage_token_hash,
			delete_token_hash,
			created_at_unix,
			expires_at_unix,
			data_policy,
			size,
			compressed_size,
			content_type
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?)
	`, meta.ID, meta.Filename, meta.PasswordHash, meta.ManageTokenHash, meta.DeleteTokenHash, meta.CreatedAt.Unix(), expiresAt, meta.DataPolicy, meta.ContentType)
	if err != nil {
		if isMySQLDuplicate(err) {
			return Metadata{}, ErrCodeExists
		}
		return Metadata{}, err
	}

	counter := &countingReader{r: r}
	chunks := newMySQLChunkWriter(tx, meta.ID)
	encoder, err := zstd.NewWriter(chunks, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(s.zstdLevel)))
	if err != nil {
		return Metadata{}, err
	}

	_, copyErr := io.Copy(encoder, counter)
	closeErr := encoder.Close()
	flushErr := chunks.Flush()
	if copyErr != nil {
		return Metadata{}, copyErr
	}
	if closeErr != nil {
		return Metadata{}, closeErr
	}
	if flushErr != nil {
		return Metadata{}, flushErr
	}

	meta.Size = counter.n

	_, err = tx.Exec(`
		UPDATE paste_metadata
		SET size = ?, compressed_size = ?
		WHERE id = ?
	`, meta.Size, chunks.compressedSize, meta.ID)
	if err != nil {
		return Metadata{}, err
	}

	if err := tx.Commit(); err != nil {
		return Metadata{}, err
	}
	tx = nil

	return meta, nil
}

func (s *Store) openMySQL(id string, password string) (*Entry, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	meta, err := s.readMySQLMetadata(id)
	if err != nil {
		return nil, ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		_ = s.deleteMySQLRows(id)
		return nil, ErrNotFound
	}

	if err := checkPassword(meta, password); err != nil {
		return nil, err
	}

	reader, err := s.openMySQLContent(id)
	if err != nil {
		return nil, err
	}

	return &Entry{Meta: meta, File: reader}, nil
}

func (s *Store) viewMySQL(id string, password string, fn func(*Entry) error) error {
	if !validID(id) {
		return ErrNotFound
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	meta, err := s.readMySQLMetadata(id)
	if err != nil {
		return ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		_ = s.deleteMySQLRows(id)
		return ErrNotFound
	}

	if err := checkPassword(meta, password); err != nil {
		return err
	}

	reader, err := s.openMySQLContent(id)
	if err != nil {
		return err
	}

	entry := &Entry{Meta: meta, File: reader}
	callbackErr := fn(entry)
	closeErr := reader.Close()
	if callbackErr != nil {
		return callbackErr
	}
	if closeErr != nil {
		return closeErr
	}

	if strings.EqualFold(meta.DataPolicy, "once") {
		return s.deleteMySQLRows(id)
	}

	return nil
}

func (s *Store) deleteMySQL(id string, token string) error {
	if !validID(id) {
		return ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidDeleteToken
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	meta, err := s.readMySQLMetadata(id)
	if err != nil {
		return ErrNotFound
	}

	if err := checkDeleteToken(meta, token); err != nil {
		return err
	}

	return s.deleteMySQLRows(id)
}

func (s *Store) deleteManagedMySQL(id string, token string) error {
	if !validID(id) {
		return ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidManageToken
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	meta, err := s.readMySQLMetadata(id)
	if err != nil {
		return ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		_ = s.deleteMySQLRows(id)
		return ErrNotFound
	}

	if err := checkManageToken(meta, token); err != nil {
		return err
	}

	return s.deleteMySQLRows(id)
}

func (s *Store) adminDeleteMySQL(id string) error {
	if !validID(id) {
		return ErrNotFound
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	return s.deleteMySQLRows(id)
}

func (s *Store) adminDeleteAllMySQL() (int, error) {
	result, err := s.mysqlDB.Exec(`DELETE FROM paste_metadata`)
	if err != nil {
		return 0, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	return int(rows), nil
}

func (s *Store) cleanupExpiredMySQL() error {
	_, err := s.mysqlDB.Exec(`
		DELETE FROM paste_metadata
		WHERE expires_at_unix IS NOT NULL
		  AND expires_at_unix < ?
	`, time.Now().UTC().Unix())
	return err
}

func (s *Store) listPastesMySQL() ([]AdminPasteItem, error) {
	rows, err := s.mysqlDB.Query(`
		SELECT
			id,
			filename,
			password_hash,
			created_at_unix,
			expires_at_unix,
			data_policy,
			size,
			content_type
		FROM paste_metadata
		ORDER BY created_at_unix DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]AdminPasteItem, 0)
	for rows.Next() {
		var id string
		var filename sql.NullString
		var passwordHash sql.NullString
		var createdAt int64
		var expiresAt sql.NullInt64
		var dataPolicy string
		var size int64
		var contentType sql.NullString

		if err := rows.Scan(&id, &filename, &passwordHash, &createdAt, &expiresAt, &dataPolicy, &size, &contentType); err != nil {
			return nil, err
		}

		items = append(items, AdminPasteItem{
			ID:          id,
			Filename:    filename.String,
			CreatedAt:   time.Unix(createdAt, 0).UTC(),
			ExpiresAt:   mysqlUnixTime(expiresAt),
			DataPolicy:  dataPolicy,
			Size:        size,
			ContentType: contentType.String,
			Protected:   passwordHash.String != "",
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (s *Store) manageMetadataMySQL(id string, token string) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return Metadata{}, ErrInvalidManageToken
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	meta, err := s.readMySQLMetadata(id)
	if err != nil {
		return Metadata{}, ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		_ = s.deleteMySQLRows(id)
		return Metadata{}, ErrNotFound
	}

	if err := checkManageToken(meta, token); err != nil {
		return Metadata{}, err
	}

	return meta, nil
}

func (s *Store) setPasswordProtectionMySQL(id string, token string) (Metadata, string, error) {
	if !validID(id) {
		return Metadata{}, "", ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return Metadata{}, "", ErrInvalidManageToken
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	meta, err := s.readMySQLMetadata(id)
	if err != nil {
		return Metadata{}, "", ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		_ = s.deleteMySQLRows(id)
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

	_, err = s.mysqlDB.Exec(`UPDATE paste_metadata SET password_hash = ? WHERE id = ?`, passwordHash, id)
	if err != nil {
		return Metadata{}, "", err
	}

	meta.PasswordHash = passwordHash
	return meta, password, nil
}

func (s *Store) clearPasswordProtectionMySQL(id string, token string, password string) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return Metadata{}, ErrInvalidManageToken
	}

	unlock := s.locks.Lock(id)
	defer unlock()

	meta, err := s.readMySQLMetadata(id)
	if err != nil {
		return Metadata{}, ErrNotFound
	}

	if isExpired(meta, time.Now().UTC()) {
		_ = s.deleteMySQLRows(id)
		return Metadata{}, ErrNotFound
	}

	if err := checkManageToken(meta, token); err != nil {
		return Metadata{}, err
	}

	if err := checkPassword(meta, password); err != nil {
		return Metadata{}, err
	}

	_, err = s.mysqlDB.Exec(`UPDATE paste_metadata SET password_hash = NULL WHERE id = ?`, id)
	if err != nil {
		return Metadata{}, err
	}

	meta.PasswordHash = ""
	return meta, nil
}

func (s *Store) setDataPolicyMySQL(id string, token string, policy string) (Metadata, error) {
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

	now := time.Now().UTC()
	meta, err := s.readMySQLMetadata(id)
	if err != nil {
		return Metadata{}, ErrNotFound
	}

	if isExpired(meta, now) {
		_ = s.deleteMySQLRows(id)
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

	_, err = s.mysqlDB.Exec(`
		UPDATE paste_metadata
		SET data_policy = ?, expires_at_unix = ?
		WHERE id = ?
	`, meta.DataPolicy, mysqlNullUnix(meta.ExpiresAt), id)
	if err != nil {
		return Metadata{}, err
	}

	return meta, nil
}

func (s *Store) readMySQLMetadata(id string) (Metadata, error) {
	var meta Metadata
	var filename sql.NullString
	var passwordHash sql.NullString
	var createdAt int64
	var expiresAt sql.NullInt64
	var contentType sql.NullString

	err := s.mysqlDB.QueryRow(`
		SELECT
			id,
			filename,
			password_hash,
			manage_token_hash,
			delete_token_hash,
			created_at_unix,
			expires_at_unix,
			data_policy,
			size,
			content_type
		FROM paste_metadata
		WHERE id = ?
	`, id).Scan(
		&meta.ID,
		&filename,
		&passwordHash,
		&meta.ManageTokenHash,
		&meta.DeleteTokenHash,
		&createdAt,
		&expiresAt,
		&meta.DataPolicy,
		&meta.Size,
		&contentType,
	)
	if err != nil {
		return Metadata{}, err
	}

	meta.Filename = filename.String
	meta.PasswordHash = passwordHash.String
	meta.CreatedAt = time.Unix(createdAt, 0).UTC()
	meta.ExpiresAt = mysqlUnixTime(expiresAt)
	meta.ContentType = contentType.String

	return meta, nil
}

func (s *Store) openMySQLContent(id string) (io.ReadCloser, error) {
	rows, err := s.mysqlDB.Query(`
		SELECT data
		FROM paste_content_chunks
		WHERE paste_id = ?
		ORDER BY chunk_index ASC
	`, id)
	if err != nil {
		return nil, err
	}

	chunks := &mysqlChunkReader{rows: rows}
	decoder, err := zstd.NewReader(chunks)
	if err != nil {
		_ = rows.Close()
		return nil, err
	}

	return &mysqlZstdReadCloser{
		decoder: decoder,
		chunks:  chunks,
	}, nil
}

func (s *Store) deleteMySQLRows(id string) error {
	result, err := s.mysqlDB.Exec(`DELETE FROM paste_metadata WHERE id = ?`, id)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}

	return nil
}

func isMySQLDuplicate(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

func mysqlNullUnix(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Unix()
}

func mysqlUnixTime(value sql.NullInt64) time.Time {
	if !value.Valid || value.Int64 == 0 {
		return time.Time{}
	}
	return time.Unix(value.Int64, 0).UTC()
}

type countingReader struct {
	r io.Reader
	n int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	return n, err
}

type mysqlChunkWriter struct {
	tx             *sql.Tx
	pasteID        string
	chunkIndex     int
	buf            []byte
	compressedSize int64
}

func newMySQLChunkWriter(tx *sql.Tx, pasteID string) *mysqlChunkWriter {
	return &mysqlChunkWriter{
		tx:      tx,
		pasteID: pasteID,
		buf:     make([]byte, 0, mysqlChunkSize),
	}
}

func (w *mysqlChunkWriter) Write(p []byte) (int, error) {
	written := len(p)
	for len(p) > 0 {
		remaining := mysqlChunkSize - len(w.buf)
		if remaining > len(p) {
			remaining = len(p)
		}

		w.buf = append(w.buf, p[:remaining]...)
		p = p[remaining:]

		if len(w.buf) == mysqlChunkSize {
			if err := w.Flush(); err != nil {
				return 0, err
			}
		}
	}

	return written, nil
}

func (w *mysqlChunkWriter) Flush() error {
	if len(w.buf) == 0 {
		return nil
	}

	data := append([]byte(nil), w.buf...)
	_, err := w.tx.Exec(`
		INSERT INTO paste_content_chunks (paste_id, chunk_index, data)
		VALUES (?, ?, ?)
	`, w.pasteID, w.chunkIndex, data)
	if err != nil {
		return err
	}

	w.compressedSize += int64(len(data))
	w.chunkIndex++
	w.buf = w.buf[:0]

	return nil
}

type mysqlChunkReader struct {
	rows    *sql.Rows
	current []byte
	offset  int
	closed  bool
}

func (r *mysqlChunkReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, io.EOF
	}

	for len(r.current) == r.offset {
		if !r.rows.Next() {
			r.closed = true
			if err := r.rows.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}

		if err := r.rows.Scan(&r.current); err != nil {
			return 0, err
		}
		r.offset = 0
	}

	n := copy(p, r.current[r.offset:])
	r.offset += n
	return n, nil
}

func (r *mysqlChunkReader) Close() error {
	r.closed = true
	return r.rows.Close()
}

type mysqlZstdReadCloser struct {
	decoder *zstd.Decoder
	chunks  *mysqlChunkReader
}

func (r *mysqlZstdReadCloser) Read(p []byte) (int, error) {
	return r.decoder.Read(p)
}

func (r *mysqlZstdReadCloser) Close() error {
	r.decoder.Close()
	return r.chunks.Close()
}
