package database

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"tubes-wbd/src/auth"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const targetConnectTimeout = 5 * time.Second

var (
	ErrInvalidConnection      = errors.New("konfigurasi koneksi tidak valid")
	ErrConnectionNotFound     = errors.New("koneksi tidak ditemukan")
	ErrConnectionInactive     = errors.New("koneksi nonaktif tidak dapat digunakan")
	ErrConnectionNameExists   = errors.New("nama koneksi sudah digunakan")
	ErrConnectionTargetExists = errors.New("target sudah terdaftar; gunakan koneksi yang sudah ada")
	ErrConnectionKey          = errors.New("kunci enkripsi koneksi harus berukuran 32 byte")
	ErrConnectionCredentials  = errors.New("kredensial koneksi tidak dapat digunakan")
	ErrTargetUnavailable      = errors.New("koneksi target gagal; periksa host, port, TLS, dan kredensial")
	ErrTargetTimeout          = errors.New("koneksi target melewati batas waktu")
	ErrConnectionStorage      = errors.New("konfigurasi koneksi tidak dapat dibaca")
)

// Connection is safe to pass to templates: it never contains a stored password.
type Connection struct {
	ID           int64
	Name         string
	Host         string
	Port         int
	DatabaseName string
	DBUsername   string
	SSLMode      string
	IsActive     bool
}

type ConnectionInput struct {
	Name         string
	Host         string
	Port         int
	DatabaseName string
	DBUsername   string
	SSLMode      string
	Password     string `json:"-"`
}

// ConnectionService owns encryption and shared target pools.
// Configuration and active status are read again before returning any pool.
type ConnectionService struct {
	platformDB     *sql.DB
	passwordCipher cipher.AEAD
	mu             sync.Mutex
	pools          map[int64]cachedTarget
	closed         bool
}

type cachedTarget struct {
	configuration storedConnection
	pool          *sql.DB
}

func NewConnectionService(platformDB *sql.DB, key []byte) (*ConnectionService, error) {
	if platformDB == nil {
		return nil, ErrConnectionStorage
	}
	if len(key) != 32 {
		return nil, ErrConnectionKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrConnectionKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrConnectionKey
	}
	return &ConnectionService{platformDB: platformDB, passwordCipher: aead, pools: make(map[int64]cachedTarget)}, nil
}

func (s *ConnectionService) List(ctx context.Context, user *auth.User) ([]Connection, error) {
	if err := auth.Authorize(ctx, s.platformDB, user, auth.ManagePlatform, 0); err != nil {
		return nil, err
	}
	return (connectionRepository{db: s.platformDB}).list(ctx)
}

func (s *ConnectionService) Find(ctx context.Context, user *auth.User, id int64) (Connection, error) {
	if err := auth.Authorize(ctx, s.platformDB, user, auth.ManagePlatform, 0); err != nil {
		return Connection{}, err
	}
	if id <= 0 {
		return Connection{}, ErrInvalidConnection
	}
	return (connectionRepository{db: s.platformDB}).find(ctx, id)
}

func (s *ConnectionService) Create(ctx context.Context, user *auth.User, input ConnectionInput) error {
	if err := auth.Authorize(ctx, s.platformDB, user, auth.ManagePlatform, 0); err != nil {
		return err
	}
	input, err := validateConnection(input, true)
	if err != nil {
		return err
	}
	encrypted, err := s.encryptPassword(input.Password)
	if err != nil {
		return err
	}
	return (connectionRepository{db: s.platformDB}).create(ctx, input, encrypted, user.ID)
}

func (s *ConnectionService) Update(ctx context.Context, user *auth.User, id int64, input ConnectionInput) error {
	if err := auth.Authorize(ctx, s.platformDB, user, auth.ManagePlatform, 0); err != nil {
		return err
	}
	if id <= 0 {
		return ErrInvalidConnection
	}
	input, err := validateConnection(input, false)
	if err != nil {
		return err
	}
	var encrypted string
	if input.Password != "" {
		encrypted, err = s.encryptPassword(input.Password)
		if err != nil {
			return err
		}
	}
	// The UPDATE itself preserves the old ciphertext when encrypted is empty.
	tx, err := s.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := (connectionRepository{db: tx}).update(ctx, id, input, encrypted); err != nil {
		return err
	}
	return s.commitConfiguration(tx, id)
}

func (s *ConnectionService) SetActive(ctx context.Context, user *auth.User, id int64, active bool) error {
	if err := auth.Authorize(ctx, s.platformDB, user, auth.ManagePlatform, 0); err != nil {
		return err
	}
	if id <= 0 {
		return ErrInvalidConnection
	}
	tx, err := s.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	repository := connectionRepository{db: tx}
	previous, err := repository.lockActive(ctx, id)
	if err != nil {
		return err
	}
	if previous == active {
		return tx.Commit()
	}
	if err := repository.setActive(ctx, id, active); err != nil {
		return err
	}
	if !active {
		ids, err := repository.affectedUserIDs(ctx, id)
		if err != nil {
			return err
		}
		if err := auth.RevokeSessions(ctx, tx, ids); err != nil {
			return err
		}
	}
	return s.commitConfiguration(tx, id)
}

// Test verifies the saved configuration. Platform Admin permission is sufficient
// to test a connection, but does not grant viewer or migration access.
func (s *ConnectionService) Test(ctx context.Context, user *auth.User, id int64) error {
	if err := auth.Authorize(ctx, s.platformDB, user, auth.ManagePlatform, 0); err != nil {
		return err
	}
	_, err := s.openRegistered(ctx, id, nil)
	if err != nil {
		return err
	}
	return nil
}

// OpenTarget checks a user's group access and returns a service-owned pool.
// Request it for each operation. Do not close it; Close manages its lifetime.
func (s *ConnectionService) OpenTarget(ctx context.Context, user *auth.User, id int64, action auth.Action) (*sql.DB, error) {
	if user == nil {
		return nil, auth.ErrForbidden
	}
	switch action {
	case auth.ViewSchema, auth.ViewData, auth.ViewMigration, auth.CreateMigration, auth.EditMigration, auth.ExecuteMigration:
	default:
		return nil, auth.ErrForbidden
	}
	return s.openRegistered(ctx, id, func(tx *sql.Tx) error { return auth.Authorize(ctx, tx, user, action, id) })
}

func (s *ConnectionService) openRegistered(ctx context.Context, id int64, authorize func(*sql.Tx) error) (*sql.DB, error) {
	if id <= 0 {
		return nil, ErrInvalidConnection
	}
	ctx, cancel := context.WithTimeout(ctx, targetConnectTimeout)
	defer cancel()
	tx, err := s.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, connectionOpenError(ctx, ErrConnectionStorage)
	}
	defer tx.Rollback()
	stored, err := (connectionRepository{db: tx}).stored(ctx, id)
	if err != nil {
		if errors.Is(err, ErrConnectionNotFound) {
			return nil, err
		}
		return nil, connectionOpenError(ctx, ErrConnectionStorage)
	}
	if !stored.IsActive {
		s.invalidateTarget(id)
		return nil, ErrConnectionInactive
	}
	if authorize != nil {
		if err := authorize(tx); err != nil {
			return nil, err
		}
	}
	// Network I/O runs outside the cache lock so another target cannot hold up
	// this operation's timeout. Row locks protect configuration while opening.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrTargetUnavailable
	}
	cached, found := s.pools[id]
	if found && cached.configuration != stored {
		delete(s.pools, id)
	}
	s.mu.Unlock()
	if found && cached.configuration == stored {
		if err := cached.pool.PingContext(ctx); err != nil {
			s.invalidatePool(id, cached.pool)
			return nil, connectionOpenError(ctx, ErrTargetUnavailable)
		}
		if err := validateTarget(ctx, tx, cached.pool); err != nil {
			s.invalidatePool(id, cached.pool)
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, connectionOpenError(ctx, ErrConnectionStorage)
		}
		return cached.pool, nil
	}
	if found {
		cached.pool.Close()
	}
	password, err := s.decryptPassword(stored.passwordEncrypted)
	if err != nil {
		return nil, err
	}
	pool, err := connectTarget(ctx, targetDSN(stored.Connection, password))
	if err != nil {
		return nil, err
	}
	if err := validateTarget(ctx, tx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	// Publish while holding the cache lock and committing the row lock. A
	// simultaneous opener may already have created a pool for this configuration.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		pool.Close()
		return nil, ErrTargetUnavailable
	}
	if cached, ok := s.pools[id]; ok && cached.configuration == stored {
		pool.Close()
		if err := tx.Commit(); err != nil {
			return nil, connectionOpenError(ctx, ErrConnectionStorage)
		}
		return cached.pool, nil
	}
	if err := tx.Commit(); err != nil {
		pool.Close()
		return nil, connectionOpenError(ctx, ErrConnectionStorage)
	}
	s.pools[id] = cachedTarget{configuration: stored, pool: pool}
	return pool, nil
}

func validateTarget(ctx context.Context, platform *sql.Tx, target *sql.DB) error {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ErrTargetUnavailable
	}
	key := -int64(binary.LittleEndian.Uint64(nonce[:])>>1) - 1
	// Lock transaksi mendeteksi database yang sama meski host atau username berbeda.
	if _, err := platform.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
		return connectionOpenError(ctx, ErrConnectionStorage)
	}
	var separate bool
	if err := target.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, key).Scan(&separate); err != nil {
		return connectionOpenError(ctx, ErrTargetUnavailable)
	}
	if !separate {
		return fmt.Errorf("%w: database platform tidak boleh menjadi target", ErrInvalidConnection)
	}
	return nil
}

// TargetProvider returns a shared, provider-owned pool. It does not authorize
// users: viewer/migration handlers must check the requested action first.
type TargetProvider func(context.Context, int64) (*sql.DB, error)

// Target adapts the service to viewer, migration, and startup recovery. Recovery
// is a system operation; user-facing callers must authorize before calling it.
func (s *ConnectionService) Target(ctx context.Context, id int64) (*sql.DB, error) {
	return s.openRegistered(ctx, id, nil)
}

// TargetConnection keeps provider/driver errors out of HTTP responses and logs.
func TargetConnection(ctx context.Context, platform *sql.DB, provider TargetProvider, id int64) (*sql.DB, error) {
	if provider == nil {
		return nil, ErrTargetUnavailable
	}
	db, err := provider(ctx, id)
	if err != nil || db == nil || db == platform {
		return nil, ErrTargetUnavailable
	}
	return db, nil
}

// commitConfiguration invalidates only after the database transaction succeeds.
// Openers hold the row lock before the cache lock, just like this transaction.
func (s *ConnectionService) commitConfiguration(tx *sql.Tx, id int64) error {
	s.mu.Lock()
	if err := tx.Commit(); err != nil {
		s.mu.Unlock()
		return err
	}
	cached := s.pools[id]
	delete(s.pools, id)
	s.mu.Unlock()
	if cached.pool != nil {
		cached.pool.Close()
	}
	return nil
}

func (s *ConnectionService) invalidateTarget(id int64) {
	s.invalidatePool(id, nil)
}

func (s *ConnectionService) invalidatePool(id int64, expected *sql.DB) {
	s.mu.Lock()
	cached := s.pools[id]
	if expected != nil && cached.pool != expected {
		s.mu.Unlock()
		return
	}
	delete(s.pools, id)
	s.mu.Unlock()
	if cached.pool != nil {
		cached.pool.Close()
	}
}

// Close releases all target pools at application shutdown. The platform pool
// belongs to the caller and remains independently managed.
func (s *ConnectionService) Close() error {
	s.mu.Lock()
	s.closed = true
	pools := s.pools
	s.pools = make(map[int64]cachedTarget)
	s.mu.Unlock()
	var failures []error
	for _, cached := range pools {
		if err := cached.pool.Close(); err != nil {
			failures = append(failures, ErrTargetUnavailable)
		}
	}
	return errors.Join(failures...)
}

func validateConnection(input ConnectionInput, creating bool) (ConnectionInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Host = strings.TrimSpace(input.Host)
	input.DatabaseName = strings.TrimSpace(input.DatabaseName)
	input.DBUsername = strings.TrimSpace(input.DBUsername)
	if input.Name == "" || !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) > 100 {
		return input, fmt.Errorf("%w: nama wajib diisi dan maksimal 100 karakter", ErrInvalidConnection)
	}
	if input.Host == "" || strings.ContainsAny(input.Host, " \t\r\n\x00") || input.DatabaseName == "" || input.DBUsername == "" {
		return input, fmt.Errorf("%w: host, nama database, dan username wajib diisi", ErrInvalidConnection)
	}
	if !utf8.ValidString(input.Host+input.DatabaseName+input.DBUsername) || strings.ContainsRune(input.DatabaseName+input.DBUsername, 0) {
		return input, ErrInvalidConnection
	}
	if input.Port < 1 || input.Port > 65535 {
		return input, fmt.Errorf("%w: port harus 1 sampai 65535", ErrInvalidConnection)
	}
	switch input.SSLMode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return input, fmt.Errorf("%w: ssl_mode tidak dikenal", ErrInvalidConnection)
	}
	if creating && input.Password == "" {
		return input, fmt.Errorf("%w: password wajib diisi", ErrInvalidConnection)
	}
	return input, nil
}

func targetDSN(connection Connection, password string) string {
	u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(connection.Host, strconv.Itoa(connection.Port)), Path: "/" + connection.DatabaseName, User: url.UserPassword(connection.DBUsername, password)}
	query := url.Values{"sslmode": {connection.SSLMode}, "connect_timeout": {"5"}, "application_name": {"wbd-target"}}
	u.RawQuery = query.Encode()
	return u.String()
}

// Connect is a low-level helper for a caller-owned DSN. Application operations
// on registered targets should use ConnectionService.OpenTarget instead.
func Connect(dsn string) (*sql.DB, error) {
	return connectTarget(context.Background(), dsn)
}

func connectTarget(ctx context.Context, dsn string) (*sql.DB, error) {
	ctx, cancel := context.WithTimeout(ctx, targetConnectTimeout)
	defer cancel()
	if strings.TrimSpace(dsn) == "" {
		return nil, ErrTargetUnavailable
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, ErrTargetUnavailable
	}
	config.ConnectTimeout = targetConnectTimeout
	pool := stdlib.OpenDB(*config)
	pool.SetMaxOpenConns(4)
	pool.SetMaxIdleConns(1)
	pool.SetConnMaxLifetime(5 * time.Minute)
	pool.SetConnMaxIdleTime(time.Minute)
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, connectionOpenError(ctx, ErrTargetUnavailable)
	}
	return pool, nil
}

func connectionOpenError(ctx context.Context, fallback error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrTargetTimeout
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	return fallback
}
