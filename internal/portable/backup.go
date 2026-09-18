package portable

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Backup requires a stopped server so SQLite's database and WAL are captured
// together with encryption keys and file storage. It never overwrites an archive.
func Backup(dataDir, archive string) (err error) {
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	info, err := os.Lstat(dataDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("data directory must be a real directory")
	}
	archive, err = filepath.Abs(archive)
	if err != nil {
		return err
	}
	dataDir, err = filepath.EvalSymlinks(dataDir)
	if err != nil {
		return err
	}
	archiveParent, err := filepath.EvalSymlinks(filepath.Dir(archive))
	if err != nil {
		return err
	}
	archive = filepath.Join(archiveParent, filepath.Base(archive))
	relative, err := filepath.Rel(dataDir, archive)
	if err != nil {
		return err
	}
	if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("backup archive must be outside the data directory")
	}
	unlock, err := LockData(dataDir)
	if err != nil {
		return fmt.Errorf("stop the server before backup: %w", err)
	}
	defer unlock()
	out, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		out.Close()
		if err != nil {
			os.Remove(archive)
		}
	}()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(dataDir, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name, err := filepath.Rel(dataDir, file)
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		name = filepath.ToSlash(name)
		if name == "logs" && entry.IsDir() {
			return filepath.SkipDir
		}
		if name == ".server.lock" || name == "ready.json" || strings.HasPrefix(name, ".ready-") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("cannot back up non-regular entry %s", name)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = name
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		if err = tw.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		in, err := os.Open(file)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		tw.Close()
		gz.Close()
		return err
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

// Restore only creates a new directory. On failure it removes exactly that
// newly-created directory; existing installations are never modified.
func Restore(archive, dataDir string) (err error) {
	in, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer gz.Close()
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	if err = os.Mkdir(dataDir, 0700); err != nil {
		return fmt.Errorf("restore requires a new destination directory: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dataDir)
		}
	}()
	unlock, lockErr := LockData(dataDir)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	tr := tar.NewReader(gz)
	for {
		header, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nextErr
		}
		name := header.Name
		cleaned := path.Clean(name)
		if name == "" || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned == "." || cleaned == ".server.lock" {
			return fmt.Errorf("unsafe archive path %q", name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA && header.Typeflag != tar.TypeDir {
			return fmt.Errorf("unsupported archive entry type for %q", name)
		}
		target := filepath.Join(dataDir, filepath.FromSlash(cleaned))
		if header.Typeflag == tar.TypeDir {
			if err = os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if header.Mode&0100 != 0 {
			mode = 0700
		}
		out, openErr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(out, tr)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	// Reading to EOF verifies the gzip checksum, including archives truncated
	// immediately after tar's end marker.
	if _, err = io.Copy(io.Discard, gz); err != nil {
		return err
	}
	return nil
}
