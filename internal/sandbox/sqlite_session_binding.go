package sandbox

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SQLiteSessionSandboxBindingStore persists remote ownership and credentials.
// Lifecycle locks and turn leases remain process-local: portable mode holds an
// exclusive data-directory lock for the lifetime of its single server process.
type SQLiteSessionSandboxBindingStore struct {
	*MemorySessionSandboxBindingStore
	db *gorm.DB
}

type sqliteSandboxBinding struct {
	TenantID       uint64 `gorm:"primaryKey;autoIncrement:false"`
	SessionID      string `gorm:"primaryKey"`
	Version        int
	Provider       RemoteProvider
	SandboxID      string
	TemplateID     string
	CreatedAt      time.Time
	ConfigID       string
	StaleAt        *time.Time
	EncryptedToken string
}

func (sqliteSandboxBinding) TableName() string { return "portable_sandbox_bindings" }

func NewSQLiteSessionSandboxBindingStore(db *gorm.DB) (*SQLiteSessionSandboxBindingStore, error) {
	if db == nil {
		return nil, errors.New("sandbox binding database is required")
	}
	if utils.GetAESKey() == nil {
		return nil, utils.ErrEncryptedDataMissingKey
	}
	if err := db.AutoMigrate(&sqliteSandboxBinding{}); err != nil {
		return nil, fmt.Errorf("migrate sandbox bindings: %w", err)
	}
	return &SQLiteSessionSandboxBindingStore{MemorySessionSandboxBindingStore: NewMemorySessionSandboxBindingStore(), db: db}, nil
}

// Encode before encryption because opaque provider tokens may themselves start
// with enc:v1:, which EncryptAESGCM treats as already encrypted.
func encryptBindingToken(token string) (string, error) {
	if utils.GetAESKey() == nil {
		return "", utils.ErrEncryptedDataMissingKey
	}
	return utils.EncryptAESGCM(base64.RawStdEncoding.EncodeToString([]byte(token)), utils.GetAESKey())
}
func (s *SQLiteSessionSandboxBindingStore) query(ctx context.Context, key SessionSandboxKey) *gorm.DB {
	return s.db.WithContext(ctx).Model(&sqliteSandboxBinding{}).Where("tenant_id = ? AND session_id = ?", key.TenantID, key.SessionID)
}
func (s *SQLiteSessionSandboxBindingStore) Get(ctx context.Context, key SessionSandboxKey) (*SessionSandboxBinding, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	var row sqliteSandboxBinding
	err := s.query(ctx, key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.EncryptedToken != "" && !strings.HasPrefix(row.EncryptedToken, utils.EncPrefix) {
		return nil, errors.New("sandbox credential is not encrypted")
	}
	encoded, err := utils.DecryptStoredSecret(row.EncryptedToken)
	if err != nil {
		return nil, fmt.Errorf("decrypt sandbox credential: %w", err)
	}
	token, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("invalid sandbox credential encoding")
	}
	result := &SessionSandboxBinding{Version: row.Version, Provider: row.Provider, TenantID: row.TenantID, SessionID: row.SessionID, SandboxID: row.SandboxID, TemplateID: row.TemplateID, CreatedAt: row.CreatedAt, ConfigID: row.ConfigID, StaleAt: row.StaleAt, TrafficAccessToken: string(token)}
	if err := result.Validate(key); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *SQLiteSessionSandboxBindingStore) Create(ctx context.Context, key SessionSandboxKey, b SessionSandboxBinding) (bool, error) {
	if err := b.Validate(key); err != nil {
		return false, err
	}
	encrypted, err := encryptBindingToken(b.TrafficAccessToken)
	if err != nil {
		return false, err
	}
	row := sqliteSandboxBinding{TenantID: key.TenantID, SessionID: key.SessionID, Version: b.Version, Provider: b.Provider, SandboxID: b.SandboxID, TemplateID: b.TemplateID, CreatedAt: b.CreatedAt, ConfigID: NormalizeConfigID(b.ConfigID), StaleAt: b.StaleAt, EncryptedToken: encrypted}
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	return result.RowsAffected > 0, result.Error
}
func (s *SQLiteSessionSandboxBindingStore) DeleteIfMatch(ctx context.Context, key SessionSandboxKey, provider RemoteProvider, id string) (bool, error) {
	if err := validateBindingMatch(key, provider, id); err != nil {
		return false, err
	}
	result := s.query(ctx, key).Where("provider = ? AND sandbox_id = ?", provider, id).Delete(&sqliteSandboxBinding{})
	return result.RowsAffected > 0, result.Error
}
func (s *SQLiteSessionSandboxBindingStore) ReplaceTrafficTokenIfMatch(ctx context.Context, key SessionSandboxKey, expected SessionSandboxBinding, token string) (bool, error) {
	if err := validateBindingMatch(key, expected.Provider, expected.SandboxID); err != nil {
		return false, err
	}
	if token == "" {
		return false, nil
	}
	// Compare the old ciphertext as well so concurrent rotations cannot overwrite
	// a credential read before a newer rotation committed.
	var row sqliteSandboxBinding
	if err := s.query(ctx, key).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if row.Provider != expected.Provider || row.SandboxID != expected.SandboxID {
		return false, nil
	}
	if row.EncryptedToken != "" && !strings.HasPrefix(row.EncryptedToken, utils.EncPrefix) {
		return false, errors.New("sandbox credential is not encrypted")
	}
	encoded, err := utils.DecryptStoredSecret(row.EncryptedToken)
	if err != nil {
		return false, err
	}
	if encoded == base64.RawStdEncoding.EncodeToString([]byte(token)) {
		return false, nil
	}
	encrypted, err := encryptBindingToken(token)
	if err != nil {
		return false, err
	}
	result := s.query(ctx, key).Where("provider = ? AND sandbox_id = ? AND encrypted_token = ?", expected.Provider, expected.SandboxID, row.EncryptedToken).Update("encrypted_token", encrypted)
	return result.RowsAffected > 0, result.Error
}
func (s *SQLiteSessionSandboxBindingStore) InvalidateByConfig(ctx context.Context, tenantID uint64, configID string) (int, error) {
	return invalidateBindingsByConfig(ctx, s, tenantID, configID)
}
func (s *SQLiteSessionSandboxBindingStore) listTenantBindingKeys(ctx context.Context, tenantID uint64) ([]SessionSandboxKey, error) {
	if tenantID == 0 {
		return nil, errors.New("sandbox binding requires tenant")
	}
	var keys []SessionSandboxKey
	err := s.db.WithContext(ctx).Model(&sqliteSandboxBinding{}).Select("tenant_id, session_id").Where("tenant_id = ?", tenantID).Find(&keys).Error
	return keys, err
}
func (s *SQLiteSessionSandboxBindingStore) markBindingStale(ctx context.Context, key SessionSandboxKey, expected SessionSandboxBinding, at time.Time) (bool, error) {
	if err := validateBindingMatch(key, expected.Provider, expected.SandboxID); err != nil {
		return false, err
	}
	result := s.query(ctx, key).Where("provider = ? AND sandbox_id = ? AND stale_at IS NULL", expected.Provider, expected.SandboxID).Update("stale_at", at)
	return result.RowsAffected > 0, result.Error
}

var _ tenantBindingScanner = (*SQLiteSessionSandboxBindingStore)(nil)
var _ sessionTurnLeaseStore = (*SQLiteSessionSandboxBindingStore)(nil)
