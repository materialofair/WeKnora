package database

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
)

func TestPortableMigrationsUseResourcesAndPreserveDirtyState(t *testing.T) {
	t.Setenv("WEKNORA_RESOURCES_DIR", sqliteRepoRoot(t))
	path := filepath.Join(t.TempDir(), "portable.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: path}))
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec("UPDATE schema_migrations SET dirty = 1")
	require.NoError(t, err)
	err = RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: path})
	require.Error(t, err)
	var dirty bool
	require.NoError(t, db.QueryRow("SELECT dirty FROM schema_migrations").Scan(&dirty))
	require.True(t, dirty, "must never silently force dirty migrations")
}
