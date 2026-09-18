package portable

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreparePersistsKeysAndPaths(t *testing.T) {
	root := t.TempDir()
	resources := t.TempDir()
	if err := os.MkdirAll(filepath.Join(resources, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, "config", "config.yaml"), []byte("server: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range environmentKeys {
		t.Setenv(key, "")
	}
	opts := Options{DataDir: root, ResourcesDir: resources}
	if err := Prepare(opts); err != nil {
		t.Fatal(err)
	}
	jwt, aes := os.Getenv("JWT_SECRET"), os.Getenv("SYSTEM_AES_KEY")
	if len(jwt) < 32 || len(aes) != 32 {
		t.Fatal("invalid keys")
	}
	if err := Prepare(opts); err != nil {
		t.Fatal(err)
	}
	if jwt != os.Getenv("JWT_SECRET") || aes != os.Getenv("SYSTEM_AES_KEY") {
		t.Fatal("keys changed")
	}
	if os.Getenv("DB_PATH") != filepath.Join(root, "weknora.db") {
		t.Fatal("wrong db path")
	}
	if os.Getenv("REDIS_ADDR") != "" {
		t.Fatal("redis enabled")
	}
}

func TestPrepareRejectsMissingResources(t *testing.T) {
	if err := Prepare(Options{DataDir: t.TempDir(), ResourcesDir: t.TempDir()}); err == nil {
		t.Fatal("expected error")
	}
}
func TestReadyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.json")
	if err := WriteReady(path, "127.0.0.1:1234"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("empty ready file")
	}
}
func TestDataDirectoryLock(t *testing.T) {
	dir := t.TempDir()
	unlock, err := LockData(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := LockData(dir); err == nil {
		second()
		t.Fatal("second server obtained same data directory")
	}
	unlock()
	unlock, err = LockData(dir)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
func TestCorruptSecretsNeverRegenerate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "secrets.json")
	raw := []byte(`{"jwt":"broken"}`)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecrets(file); err == nil {
		t.Fatal("accepted corrupt secrets")
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(raw) {
		t.Fatal("overwrote existing keys")
	}
}
func TestExistingDatabaseWithoutSecretsFailsClosed(t *testing.T) {
	data, resources := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(resources, "config"), 0700)
	os.WriteFile(filepath.Join(resources, "config", "config.yaml"), []byte("server: {}"), 0600)
	if err := os.WriteFile(filepath.Join(data, "weknora.db"), []byte("existing database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(Options{DataDir: data, ResourcesDir: resources}); err == nil {
		t.Fatal("generated new keys for existing database")
	}
	if _, err := os.Stat(filepath.Join(data, "secrets.json")); !os.IsNotExist(err) {
		t.Fatal("created replacement secrets")
	}
}
