package internal

import (
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
)

type Store struct {
	DataDir        string
	TTL            time.Duration
	StorageBackend string
	locks          *lockManager
	adminDB        *sql.DB
	mysqlDB        *sql.DB
	zstdLevel      int
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
	var err error
	policy, err = policy.validated()
	if err != nil {
		return Metadata{}, "", "", "", err
	}

	return s.createFromReader(r, filename, contentType, usePassword, policy, customCode)
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

	return s.createFromReader(entry.File, entry.Meta.Filename, entry.Meta.ContentType, usePassword, policy, customCode)
}

func (s *Store) createFromReader(r io.Reader, filename string, contentType string, usePassword bool, policy DataPolicy, customCode string) (Metadata, string, string, string, error) {
	if s.StorageBackend == "mysql" {
		return s.createMySQLFromReader(r, filename, contentType, usePassword, policy, customCode)
	}

	return s.createLocalFromReader(r, filename, contentType, usePassword, policy, customCode)
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
	if s.StorageBackend == "mysql" {
		return s.deleteMySQL(id, token)
	}

	return s.deleteLocal(id, token)
}

func (s *Store) DeleteManaged(id string, token string) error {
	if s.StorageBackend == "mysql" {
		return s.deleteManagedMySQL(id, token)
	}

	return s.deleteManagedLocal(id, token)
}

func (s *Store) AdminDelete(id string) error {
	if s.StorageBackend == "mysql" {
		return s.adminDeleteMySQL(id)
	}

	return s.adminDeleteLocal(id)
}

func (s *Store) AdminDeleteAll() (int, error) {
	if s.StorageBackend == "mysql" {
		return s.adminDeleteAllMySQL()
	}

	return s.adminDeleteAllLocal()
}

func (s *Store) CleanupExpired() error {
	if s.StorageBackend == "mysql" {
		return s.cleanupExpiredMySQL()
	}

	return s.cleanupExpiredLocal()
}

func (s *Store) ListPastes() ([]AdminPasteItem, error) {
	if s.StorageBackend == "mysql" {
		return s.listPastesMySQL()
	}

	return s.listPastesLocal()
}

func (s *Store) ManageMetadata(id string, token string) (Metadata, error) {
	if s.StorageBackend == "mysql" {
		return s.manageMetadataMySQL(id, token)
	}

	return s.manageMetadataLocal(id, token)
}

func (s *Store) SetPasswordProtection(id string, token string) (Metadata, string, error) {
	if s.StorageBackend == "mysql" {
		return s.setPasswordProtectionMySQL(id, token)
	}

	return s.setPasswordProtectionLocal(id, token)
}

func (s *Store) ClearPasswordProtection(id string, token string, password string) (Metadata, error) {
	if s.StorageBackend == "mysql" {
		return s.clearPasswordProtectionMySQL(id, token, password)
	}

	return s.clearPasswordProtectionLocal(id, token, password)
}

func (s *Store) SetDataPolicy(id string, token string, policy string) (Metadata, error) {
	if s.StorageBackend == "mysql" {
		return s.setDataPolicyMySQL(id, token, policy)
	}

	return s.setDataPolicyLocal(id, token, policy)
}
