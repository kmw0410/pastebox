package internal

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
	ErrInvalidLabel       = errors.New("invalid label")
)

const pasteListCacheTTL = 5 * time.Second

type Store struct {
	DataDir        string
	TTL            time.Duration
	StorageBackend string
	locks          *lockManager
	adminDB        *sql.DB
	mysqlDB        *sql.DB
	zstdLevel      int
	listCacheMu    sync.RWMutex
	listCache      []AdminPasteItem
	listCacheUntil time.Time
	listCacheGen   uint64
}

type StoreOptions struct {
	DataDir                    string
	TTL                        time.Duration
	StorageBackend             string
	MySQLDSN                   string
	ZstdLevel                  int
	MigrateLocalPastes         bool
	MigrateSQLiteAdminAccounts bool
}

type Metadata struct {
	ID              string    `json:"id"`
	Filename        string    `json:"filename,omitempty"`
	Label           string    `json:"label,omitempty"`
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
	File io.ReadCloser
}

type AdminPasteItem struct {
	ID          string
	Filename    string
	Label       string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	DataPolicy  string
	Size        int64
	ContentType string
	Protected   bool
}

func NewStore(dataDir string, ttl time.Duration) (*Store, error) {
	return NewStoreWithOptions(StoreOptions{
		DataDir:        dataDir,
		TTL:            ttl,
		StorageBackend: "local",
	})
}

func NewStoreWithOptions(opts StoreOptions) (*Store, error) {
	dataDir := strings.TrimSpace(opts.DataDir)
	if dataDir == "" {
		dataDir = "/paste-data"
	}

	backend := strings.ToLower(strings.TrimSpace(opts.StorageBackend))
	if backend == "" {
		backend = "local"
	}
	if backend != "local" && backend != "mysql" {
		return nil, errors.New("invalid storage backend")
	}

	zstdLevel := opts.ZstdLevel
	if zstdLevel == 0 {
		zstdLevel = 3
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}

	adminDB, err := openAdminDB(filepath.Join(dataDir, "pastebox.db"))
	if err != nil {
		return nil, err
	}

	store := &Store{
		DataDir:        dataDir,
		TTL:            opts.TTL,
		StorageBackend: backend,
		locks:          newLockManager(),
		adminDB:        adminDB,
		zstdLevel:      zstdLevel,
	}

	if backend == "mysql" {
		mysqlDB, err := openMySQLPasteDB(opts.MySQLDSN)
		if err != nil {
			_ = adminDB.Close()
			return nil, err
		}
		store.mysqlDB = mysqlDB

		if opts.MigrateSQLiteAdminAccounts {
			if err := store.migrateSQLiteAdminAccountsToMySQL(); err != nil {
				_ = mysqlDB.Close()
				_ = adminDB.Close()
				return nil, err
			}
		}

		if opts.MigrateLocalPastes {
			if err := store.migrateLocalPastesToMySQL(); err != nil {
				_ = mysqlDB.Close()
				_ = adminDB.Close()
				return nil, err
			}
		}
	}

	return store, nil
}

func (s *Store) Create(r io.Reader, filename string, contentType string, usePassword bool, policy DataPolicy, customCode string) (Metadata, string, string, string, error) {
	return s.CreateWithLabel(r, filename, contentType, usePassword, policy, customCode, "")
}

func (s *Store) CreateWithLabel(r io.Reader, filename string, contentType string, usePassword bool, policy DataPolicy, customCode string, label string) (Metadata, string, string, string, error) {
	var err error
	policy, err = policy.validated()
	if err != nil {
		return Metadata{}, "", "", "", err
	}

	label, err = normalizeLabel(label)
	if err != nil {
		return Metadata{}, "", "", "", err
	}

	meta, password, deleteToken, manageToken, err := s.createFromReader(r, filename, contentType, usePassword, policy, customCode, label)
	if err == nil {
		s.invalidatePasteList()
	}
	return meta, password, deleteToken, manageToken, err
}

func (s *Store) Clone(id string, password string, usePassword bool, policy DataPolicy, customCode string) (Metadata, string, string, string, error) {
	var err error
	policy, err = policy.validated()
	if err != nil {
		return Metadata{}, "", "", "", err
	}

	entry, err := s.Open(id, password)
	if err != nil {
		return Metadata{}, "", "", "", err
	}
	defer entry.File.Close()

	meta, generatedPassword, deleteToken, manageToken, err := s.createFromReader(entry.File, entry.Meta.Filename, entry.Meta.ContentType, usePassword, policy, customCode, entry.Meta.Label)
	if err == nil {
		s.invalidatePasteList()
	}
	return meta, generatedPassword, deleteToken, manageToken, err
}

func (s *Store) createFromReader(r io.Reader, filename string, contentType string, usePassword bool, policy DataPolicy, customCode string, label string) (Metadata, string, string, string, error) {
	if s.StorageBackend == "mysql" {
		return s.createMySQLFromReader(r, filename, contentType, usePassword, policy, customCode, label)
	}

	return s.createLocalFromReader(r, filename, contentType, usePassword, policy, customCode, label)
}

func (s *Store) Open(id string, password string) (*Entry, error) {
	if s.StorageBackend == "mysql" {
		return s.openMySQL(id, password)
	}

	return s.openLocal(id, password)
}

func (s *Store) View(id string, password string, fn func(*Entry) error) error {
	if s.StorageBackend == "mysql" {
		return s.viewMySQL(id, password, fn)
	}

	return s.viewLocal(id, password, fn)
}

func (s *Store) Delete(id string, token string) error {
	var err error
	if s.StorageBackend == "mysql" {
		err = s.deleteMySQL(id, token)
	} else {
		err = s.deleteLocal(id, token)
	}
	if err == nil {
		s.invalidatePasteList()
	}
	return err
}

func (s *Store) DeleteManaged(id string, token string) error {
	var err error
	if s.StorageBackend == "mysql" {
		err = s.deleteManagedMySQL(id, token)
	} else {
		err = s.deleteManagedLocal(id, token)
	}
	if err == nil {
		s.invalidatePasteList()
	}
	return err
}

func (s *Store) AdminDelete(id string) error {
	var err error
	if s.StorageBackend == "mysql" {
		err = s.adminDeleteMySQL(id)
	} else {
		err = s.adminDeleteLocal(id)
	}
	if err == nil {
		s.invalidatePasteList()
	}
	return err
}

func (s *Store) AdminDeleteAll() (int, error) {
	var count int
	var err error
	if s.StorageBackend == "mysql" {
		count, err = s.adminDeleteAllMySQL()
	} else {
		count, err = s.adminDeleteAllLocal()
	}
	if err == nil {
		s.invalidatePasteList()
	}
	return count, err
}

func (s *Store) CleanupExpired() error {
	var err error
	if s.StorageBackend == "mysql" {
		err = s.cleanupExpiredMySQL()
	} else {
		err = s.cleanupExpiredLocal()
	}
	if err == nil {
		s.invalidatePasteList()
	}
	return err
}

func (s *Store) ListPastes() ([]AdminPasteItem, error) {
	if items, ok := s.cachedPasteList(time.Now()); ok {
		return items, nil
	}
	cacheGeneration := s.pasteListGeneration()

	var items []AdminPasteItem
	var err error
	if s.StorageBackend == "mysql" {
		items, err = s.listPastesMySQL()
	} else {
		items, err = s.listPastesLocal()
	}
	if err != nil {
		return nil, err
	}
	s.cachePasteList(items, time.Now().Add(pasteListCacheTTL), cacheGeneration)
	return cloneAdminPasteItems(items), nil
}

func (s *Store) ManageMetadata(id string, token string) (Metadata, error) {
	if s.StorageBackend == "mysql" {
		return s.manageMetadataMySQL(id, token)
	}

	return s.manageMetadataLocal(id, token)
}

func (s *Store) SetPasswordProtection(id string, token string) (Metadata, string, error) {
	var meta Metadata
	var password string
	var err error
	if s.StorageBackend == "mysql" {
		meta, password, err = s.setPasswordProtectionMySQL(id, token)
	} else {
		meta, password, err = s.setPasswordProtectionLocal(id, token)
	}
	if err == nil {
		s.invalidatePasteList()
	}
	return meta, password, err
}

func (s *Store) ClearPasswordProtection(id string, token string, password string) (Metadata, error) {
	var meta Metadata
	var err error
	if s.StorageBackend == "mysql" {
		meta, err = s.clearPasswordProtectionMySQL(id, token, password)
	} else {
		meta, err = s.clearPasswordProtectionLocal(id, token, password)
	}
	if err == nil {
		s.invalidatePasteList()
	}
	return meta, err
}

func (s *Store) SetDataPolicy(id string, token string, policy string) (Metadata, error) {
	var meta Metadata
	var err error
	if s.StorageBackend == "mysql" {
		meta, err = s.setDataPolicyMySQL(id, token, policy)
	} else {
		meta, err = s.setDataPolicyLocal(id, token, policy)
	}
	if err == nil {
		s.invalidatePasteList()
	}
	return meta, err
}

func (s *Store) SetLabel(id string, token string, label string) (Metadata, error) {
	label, err := normalizeLabel(label)
	if err != nil {
		return Metadata{}, err
	}
	var meta Metadata
	if s.StorageBackend == "mysql" {
		meta, err = s.setLabelMySQL(id, token, label)
	} else {
		meta, err = s.setLabelLocal(id, token, label)
	}
	if err == nil {
		s.invalidatePasteList()
	}
	return meta, err
}

func (s *Store) cachedPasteList(now time.Time) ([]AdminPasteItem, bool) {
	s.listCacheMu.RLock()
	defer s.listCacheMu.RUnlock()
	if !now.Before(s.listCacheUntil) {
		return nil, false
	}
	return cloneAdminPasteItems(s.listCache), true
}

func (s *Store) cachePasteList(items []AdminPasteItem, until time.Time, generation uint64) {
	s.listCacheMu.Lock()
	if s.listCacheGen == generation {
		s.listCache = cloneAdminPasteItems(items)
		s.listCacheUntil = until
	}
	s.listCacheMu.Unlock()
}

func (s *Store) invalidatePasteList() {
	s.listCacheMu.Lock()
	s.listCache = nil
	s.listCacheUntil = time.Time{}
	s.listCacheGen++
	s.listCacheMu.Unlock()
}

func (s *Store) pasteListGeneration() uint64 {
	s.listCacheMu.RLock()
	defer s.listCacheMu.RUnlock()
	return s.listCacheGen
}

func cloneAdminPasteItems(items []AdminPasteItem) []AdminPasteItem {
	return append([]AdminPasteItem(nil), items...)
}

func (s *Store) HealthCheck(ctx context.Context) error {
	if s.adminDB == nil {
		return errors.New("admin database is not initialized")
	}

	if err := s.adminDB.PingContext(ctx); err != nil {
		return err
	}

	if s.StorageBackend == "mysql" {
		if s.mysqlDB == nil {
			return errors.New("mysql database is not initialized")
		}
		if err := s.mysqlDB.PingContext(ctx); err != nil {
			return err
		}
	}

	return nil
}
