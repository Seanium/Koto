package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// A macOS bundle can change resources without changing its executable. Hash
// the complete tree so interrupted exchange and rollback still distinguish the
// old bundle from the new one. Symlinks are hashed, never traversed.
func targetHash(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().IsRegular() {
		return fileHash(path)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("update target is not a regular file or directory")
	}
	h := sha256.New()
	err = filepath.WalkDir(path, func(entry string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, e := filepath.Rel(path, entry)
		if e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		fmt.Fprintf(h, "%d:%s:%d:", len(rel), rel, info.Mode())
		switch {
		case info.Mode().IsRegular():
			hash, e := fileHash(entry)
			if e != nil {
				return e
			}
			fmt.Fprint(h, hash)
		case info.Mode()&os.ModeSymlink != 0:
			link, e := os.Readlink(entry)
			if e != nil {
				return e
			}
			fmt.Fprintf(h, "%d:%s", len(link), link)
		case info.IsDir():
		default:
			return fmt.Errorf("unsupported file in update bundle: %s", entry)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), err
}
