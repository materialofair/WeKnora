package sandbox

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteBindingsPersistenceIsolationAndCAS(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	path := filepath.Join(t.TempDir(), "bindings.db")
	open := func() (*gorm.DB, *SQLiteSessionSandboxBindingStore) {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		store, err := NewSQLiteSessionSandboxBindingStore(db)
		require.NoError(t, err)
		return db, store
	}
	db, s := open()
	ctx := context.Background()
	key := SessionSandboxKey{TenantID: 1, SessionID: "session"}
	b := SessionSandboxBinding{Version: 1, Provider: SandboxTypeE2B, TenantID: 1, SessionID: "session", SandboxID: "remote", TemplateID: "template", CreatedAt: time.Now().UTC(), ConfigID: "config", TrafficAccessToken: "enc:v1:opaque-provider-secret"}
	made, err := s.Create(ctx, key, b)
	require.NoError(t, err)
	require.True(t, made)
	made, err = s.Create(ctx, key, b)
	require.NoError(t, err)
	require.False(t, made)
	otherKey := SessionSandboxKey{TenantID: 2, SessionID: "session"}
	other := b
	other.TenantID = 2
	made, err = s.Create(ctx, otherKey, other)
	require.NoError(t, err)
	require.True(t, made)
	var row sqliteSandboxBinding
	require.NoError(t, db.Where("tenant_id = ?", 1).Take(&row).Error)
	require.True(t, strings.HasPrefix(row.EncryptedToken, utils.EncPrefix))
	require.NotContains(t, row.EncryptedToken, "opaque-provider-secret")
	handle, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, handle.Close())
	db, s = open()
	defer func() { h, _ := db.DB(); _ = h.Close() }()
	got, err := s.Get(ctx, key)
	require.NoError(t, err)
	require.Equal(t, b.TrafficAccessToken, got.TrafficAccessToken)
	missing, err := s.Get(ctx, SessionSandboxKey{TenantID: 3, SessionID: "session"})
	require.NoError(t, err)
	require.Nil(t, missing)
	changed, err := s.DeleteIfMatch(ctx, key, SandboxTypeCube, "remote")
	require.NoError(t, err)
	require.False(t, changed)
	wrong := b
	wrong.SandboxID = "other"
	changed, err = s.ReplaceTrafficTokenIfMatch(ctx, key, wrong, "new")
	require.NoError(t, err)
	require.False(t, changed)
	count, err := s.InvalidateByConfig(ctx, 1, "config")
	require.NoError(t, err)
	require.Equal(t, 1, count)
	count, err = s.InvalidateByConfig(ctx, 1, "config")
	require.NoError(t, err)
	require.Zero(t, count)
	changed, err = s.ReplaceTrafficTokenIfMatch(ctx, key, b, "rotated")
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = s.ReplaceTrafficTokenIfMatch(ctx, key, b, "rotated")
	require.NoError(t, err)
	require.False(t, changed)
	got, err = s.Get(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got.StaleAt)
	require.Equal(t, "rotated", got.TrafficAccessToken)
	untouched, err := s.Get(ctx, otherKey)
	require.NoError(t, err)
	require.Nil(t, untouched.StaleAt)
	require.Equal(t, b.TrafficAccessToken, untouched.TrafficAccessToken)
	require.NoError(t, s.BeginTurn(ctx, key))
	active, rebuild, err := s.TurnState(ctx, key)
	require.NoError(t, err)
	require.True(t, active)
	require.True(t, rebuild)
	require.NoError(t, s.ConsumeTurnRebuild(ctx, key))
	require.NoError(t, s.EndTurn(ctx, key))
	changed, err = s.DeleteIfMatch(ctx, key, b.Provider, b.SandboxID)
	require.NoError(t, err)
	require.True(t, changed)
	got, err = s.Get(ctx, key)
	require.NoError(t, err)
	require.Nil(t, got)
}
func TestSQLiteBindingsFailLoudly(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")
	_, err := NewSQLiteSessionSandboxBindingStore(nil)
	require.Error(t, err)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "bindings.db")), &gorm.Config{})
	require.NoError(t, err)
	_, err = NewSQLiteSessionSandboxBindingStore(db)
	require.ErrorIs(t, err, utils.ErrEncryptedDataMissingKey)
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	s, err := NewSQLiteSessionSandboxBindingStore(db)
	require.NoError(t, err)
	ctx := context.Background()
	key := SessionSandboxKey{TenantID: 1, SessionID: "session"}
	b := SessionSandboxBinding{Version: 1, Provider: SandboxTypeE2B, TenantID: 1, SessionID: "session", SandboxID: "remote", TemplateID: "template", CreatedAt: time.Now(), TrafficAccessToken: "secret"}
	_, err = s.Create(ctx, key, b)
	require.NoError(t, err)
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("x", 32))
	_, err = s.Get(ctx, key)
	require.Error(t, err)
	t.Setenv("SYSTEM_AES_KEY", "")
	_, err = s.Get(ctx, key)
	require.ErrorIs(t, err, utils.ErrEncryptedDataMissingKey)
	_, err = s.Create(ctx, key, b)
	require.ErrorIs(t, err, utils.ErrEncryptedDataMissingKey)
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	require.NoError(t, db.Model(&sqliteSandboxBinding{}).Where("tenant_id = ?", 1).Update("encrypted_token", "plaintext").Error)
	_, err = s.Get(ctx, key)
	require.Error(t, err)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.Get(cancelled, key)
	require.ErrorIs(t, err, context.Canceled)
	_, err = s.Get(ctx, SessionSandboxKey{})
	require.Error(t, err)
	handle, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, handle.Close())
	_, err = s.Get(ctx, key)
	require.Error(t, err)
	_, err = NewSQLiteSessionSandboxBindingStore(db)
	require.Error(t, err)
}
