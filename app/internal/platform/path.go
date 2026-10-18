package platform

import (
	"runtime"
	"strings"
)

// ProjectPath expresses Windows extended drive/UNC paths in the ordinary form
// consumed by Agent project-folder encoders and shells. It is for new target
// working directories, never for rewriting historical source records.
func ProjectPath(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}
	return windowsProjectPath(path)
}

func windowsProjectPath(path string) string {
	if !strings.HasPrefix(path, `\\?\`) {
		return path
	}
	rest := path[4:]
	if len(rest) >= 3 && ((rest[0] >= 'A' && rest[0] <= 'Z') || (rest[0] >= 'a' && rest[0] <= 'z')) && rest[1] == ':' && (rest[2] == '\\' || rest[2] == '/') {
		return rest
	}
	if len(rest) > 4 && strings.EqualFold(rest[:4], `UNC\`) {
		parts := strings.Split(rest[4:], `\`)
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return `\\` + rest[4:]
		}
	}
	// Device and volume namespaces have no equivalent ordinary project path.
	return path
}
