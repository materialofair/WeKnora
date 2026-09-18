package portable

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupRestorePreservesData(t *testing.T) {
	source := t.TempDir()
	os.MkdirAll(filepath.Join(source, "files"), 0700)
	os.MkdirAll(filepath.Join(source, "logs"), 0700)
	for name, body := range map[string]string{"weknora.db": "db", "secrets.json": "secret", "files/document.txt": "document", "logs/server.log": "log"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := Backup(source, archive); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if err := Restore(archive, target); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"weknora.db": "db", "secrets.json": "secret", "files/document.txt": "document"} {
		got, err := os.ReadFile(filepath.Join(target, name))
		if err != nil || string(got) != want {
			t.Fatalf("restore %s: %q %v", name, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "logs")); !os.IsNotExist(err) {
		t.Fatal("logs should not be included")
	}
	if err := Restore(archive, target); err == nil {
		t.Fatal("overwrote existing destination")
	}
}
func TestBackupRefusesActiveServer(t *testing.T) {
	dir := t.TempDir()
	unlock, err := LockData(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err = Backup(dir, filepath.Join(t.TempDir(), "backup.tar.gz")); err == nil {
		t.Fatal("backed up running server")
	}
}
func TestRestoreRejectsUnsafeEntriesAndCleansTarget(t *testing.T) {
	for _, entry := range []struct {
		name string
		kind byte
	}{{"../outside", tar.TypeReg}, {"/absolute", tar.TypeReg}, {"a/../../outside", tar.TypeReg}, {"C:/outside", tar.TypeReg}, {"..\\outside", tar.TypeReg}, {"link", tar.TypeSymlink}, {"link", tar.TypeLink}, {".server.lock", tar.TypeReg}} {
		t.Run(entry.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "bad.tar.gz")
			f, _ := os.Create(archive)
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			tw.WriteHeader(&tar.Header{Name: entry.name, Typeflag: entry.kind, Mode: 0600, Linkname: "/tmp/outside"})
			tw.Close()
			gz.Close()
			f.Close()
			target := filepath.Join(t.TempDir(), "new")
			if err := Restore(archive, target); err == nil {
				t.Fatal("accepted unsafe archive")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("failed restore left partial data")
			}
		})
	}
}
func TestBackupRejectsSymlinkAndTruncatedRestore(t *testing.T) {
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "db"), []byte("content"), 0600)
	link := filepath.Join(data, "link")
	if err := os.Symlink(filepath.Join(data, "db"), link); err == nil {
		archive := filepath.Join(t.TempDir(), "bad.tar.gz")
		if err = Backup(data, archive); err == nil {
			t.Fatal("accepted symlink")
		}
		if _, err = os.Stat(archive); !os.IsNotExist(err) {
			t.Fatal("left incomplete backup")
		}
		os.Remove(link)
	}
	archive := filepath.Join(t.TempDir(), "good.tar.gz")
	if err := Backup(data, archive); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(archive, raw[:len(raw)-5], 0600)
	target := filepath.Join(t.TempDir(), "target")
	if err = Restore(archive, target); err == nil {
		t.Fatal("accepted truncated archive")
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("left truncated restore")
	}
}
