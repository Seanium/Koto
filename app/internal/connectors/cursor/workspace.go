package cursor

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Cursor's folder workspace identity follows VS Code: MD5(fsPath + inode on
// Linux, or fsPath + creation milliseconds on macOS/Windows). A URI hash is a
// different identity and hides the first copied conversation in an empty folder.
func folderWorkspaceID(folder string) (string, error) {
	info, err := os.Stat(folder)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project path is not a directory")
	}
	salt, err := folderWorkspaceSalt(info)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" && len(filepath.VolumeName(folder)) == 2 {
		// URI.fsPath lowercases the drive letter in the official runtime.
		folder = strings.ToLower(folder[:1]) + folder[1:]
	}
	hash := md5.Sum([]byte(folder + salt))
	return hex.EncodeToString(hash[:]), nil
}
