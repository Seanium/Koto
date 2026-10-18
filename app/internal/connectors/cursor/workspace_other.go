//go:build !darwin && !linux && !windows

package cursor

import (
	"fmt"
	"os"
)

func folderWorkspaceSalt(os.FileInfo) (string, error) {
	return "", fmt.Errorf("Cursor IDE workspace identity is unsupported on this platform")
}
