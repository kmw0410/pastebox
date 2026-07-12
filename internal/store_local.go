package internal

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) createLocalFromReader(r io.Reader, filename string, contentType string, usePassword bool, policy DataPolicy, customCode string, label string) (Metadata, string, string, string, error) {
	id, path, err := s.reserveLocalPath(customCode)
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

	policy = policy.normalized()
	expiresAt := policy.ExpiresAt(now, s.TTL)

	meta := Metadata{
		ID:              id,
		Filename:        strings.TrimSpace(filename),
		Label:           label,
		PasswordHash:    passwordHash,
		ManageTokenHash: hashSecret(manageToken),
		DeleteTokenHash: hashSecret(deleteToken),
		CreatedAt:       now,
		ExpiresAt:       expiresAt,
		DataPolicy:      policy.Name,
		Size:            size,
		ContentType:     contentType,
	}

	if err := s.writeMetadata(meta); err != nil {
		_ = os.Remove(path)
		return Metadata{}, "", "", "", err
	}

	return meta, password, deleteToken, manageToken, nil
}

func (s *Store) openLocal(id string, password string) (*Entry, error) {
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

func (s *Store) viewLocal(id string, password string, fn func(*Entry) error) error {
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

func (s *Store) deleteLocal(id string, token string) error {
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

func (s *Store) deleteManagedLocal(id string, token string) error {
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

func (s *Store) adminDeleteLocal(id string) error {
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

func (s *Store) adminDeleteAllLocal() (int, error) {
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

func (s *Store) cleanupExpiredLocal() error {
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

func (s *Store) listPastesLocal() ([]AdminPasteItem, error) {
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
			Label:       meta.Label,
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

func (s *Store) setLabelLocal(id string, token string, label string) (Metadata, error) {
	meta, err := s.manageMetadataLocal(id, token)
	if err != nil {
		return Metadata{}, err
	}

	unlock := s.locks.Lock(id)
	defer unlock()
	meta.Label = label
	if err := s.writeMetadata(meta); err != nil {
		return Metadata{}, err
	}
	return meta, nil
}

func (s *Store) manageMetadataLocal(id string, token string) (Metadata, error) {
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

func (s *Store) setPasswordProtectionLocal(id string, token string) (Metadata, string, error) {
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

func (s *Store) clearPasswordProtectionLocal(id string, token string, password string) (Metadata, error) {
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

func (s *Store) setDataPolicyLocal(id string, token string, policy string) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, ErrNotFound
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return Metadata{}, ErrInvalidManageToken
	}

	parsedPolicy, err := ParseDataPolicy(policy)
	if err != nil {
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

	meta.DataPolicy = parsedPolicy.Name
	meta.ExpiresAt = parsedPolicy.ExpiresAt(now, s.TTL)

	if err := s.writeMetadata(meta); err != nil {
		return Metadata{}, err
	}

	return meta, nil
}

func (s *Store) reserveLocalPath(customCode string) (string, string, error) {
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
