// Package portable configures a self-contained deployment without external databases.
package portable

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Options struct{ DataDir, ResourcesDir string }

var environmentKeys = []string{"WEKNORA_PORTABLE", "WEKNORA_RESOURCES_DIR", "WEKNORA_CONFIG_FILE", "WEKNORA_WEB_DIR", "DB_DRIVER", "DB_PATH", "RETRIEVE_DRIVER", "STORAGE_TYPE", "LOCAL_STORAGE_BASE_DIR", "STREAM_MANAGER_TYPE", "REDIS_ADDR", "AUTO_MIGRATE", "AUTO_RECOVER_DIRTY", "JWT_SECRET", "SYSTEM_AES_KEY", "LOG_PATH", "GRAPH_DRIVER", "JIEBA_DICT_DIR"}

// Prepare is called before configuration or services are initialized. Secrets live
// beside the database and must be included in backups to keep credentials readable.
func Prepare(opts Options) error {
	if opts.DataDir == "" {
		return fmt.Errorf("portable mode requires --data-dir")
	}
	data, err := filepath.Abs(opts.DataDir)
	if err != nil {
		return err
	}
	resources := opts.ResourcesDir
	if resources == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		resources = filepath.Dir(exe)
	}
	resources, err = filepath.Abs(resources)
	if err != nil {
		return err
	}
	cfg := filepath.Join(resources, "config", "config.yaml")
	if _, err = os.Stat(cfg); err != nil {
		return fmt.Errorf("portable configuration %s: %w", cfg, err)
	}
	for _, dir := range []string{data, filepath.Join(data, "files"), filepath.Join(data, "logs")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	// Verify writable data storage before initializing any services.
	probe, err := os.CreateTemp(data, ".write-check-")
	if err != nil {
		return fmt.Errorf("data directory is not writable: %w", err)
	}
	probe.Close()
	os.Remove(probe.Name())
	secretsPath := filepath.Join(data, "secrets.json")
	if _, secretErr := os.Stat(secretsPath); os.IsNotExist(secretErr) {
		if _, dbErr := os.Stat(filepath.Join(data, "weknora.db")); dbErr == nil {
			return fmt.Errorf("existing database has no secrets.json; restore its original keys from backup before starting")
		} else if !os.IsNotExist(dbErr) {
			return dbErr
		}
	}
	keys, err := loadSecrets(secretsPath)
	if err != nil {
		return err
	}
	env := map[string]string{
		"WEKNORA_PORTABLE": "true", "WEKNORA_RESOURCES_DIR": resources, "WEKNORA_CONFIG_FILE": cfg,
		"WEKNORA_WEB_DIR": filepath.Join(resources, "web"), "DB_DRIVER": "sqlite", "DB_PATH": filepath.Join(data, "weknora.db"),
		"RETRIEVE_DRIVER": "sqlite", "STORAGE_TYPE": "local", "LOCAL_STORAGE_BASE_DIR": filepath.Join(data, "files"),
		"STREAM_MANAGER_TYPE": "memory", "REDIS_ADDR": "", "AUTO_MIGRATE": "true", "AUTO_RECOVER_DIRTY": "false",
		"JWT_SECRET": keys.JWT, "SYSTEM_AES_KEY": keys.AES, "LOG_PATH": filepath.Join(data, "logs", "weknora.log"), "GRAPH_DRIVER": "sqlite",
	}
	if os.Getenv("JIEBA_DICT_DIR") == "" {
		if _, err := os.Stat(filepath.Join(resources, "jieba", "jieba.dict.utf8")); err == nil {
			env["JIEBA_DICT_DIR"] = filepath.Join(resources, "jieba")
		}
	}
	for key, value := range env {
		if err = os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}

type secrets struct {
	JWT string `json:"jwt"`
	AES string `json:"aes"`
}

func loadSecrets(path string) (secrets, error) {
	var keys secrets
	b, err := os.ReadFile(path)
	if err == nil {
		if json.Unmarshal(b, &keys) != nil || len(keys.JWT) < 32 || len(keys.AES) != 32 {
			return keys, fmt.Errorf("invalid portable secrets file %s; restore it from backup", path)
		}
		return keys, nil
	}
	if !os.IsNotExist(err) {
		return keys, err
	}
	jwt := make([]byte, 32)
	aes := make([]byte, 16)
	if _, err = rand.Read(jwt); err != nil {
		return keys, err
	}
	if _, err = rand.Read(aes); err != nil {
		return keys, err
	}
	keys = secrets{JWT: hex.EncodeToString(jwt), AES: hex.EncodeToString(aes)}
	b, err = json.Marshal(keys)
	if err != nil {
		return keys, err
	}
	// Exclusive creation prevents another launch from replacing encryption keys.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		return loadSecrets(path)
	}
	if err != nil {
		return keys, err
	}
	_, writeErr := f.Write(b)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		return keys, writeErr
	}
	if syncErr != nil {
		return keys, syncErr
	}
	return keys, closeErr
}

// WriteReady atomically publishes the actual address after a successful bind.
func WriteReady(path, address string) error {
	if path == "" {
		return nil
	}
	b, err := json.Marshal(struct {
		URL string `json:"url"`
		PID int    `json:"pid"`
	}{"http://" + address, os.Getpid()})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ready-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
