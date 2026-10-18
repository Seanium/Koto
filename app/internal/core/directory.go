package core

// CurrentDirectory is a source application's current, explicitly associated
// working directory. It is independent of historical cwd fields on messages.
func CurrentDirectory(meta JsonObject) string { return S(meta["currentCwd"]) }

func WorkingDirectory(meta JsonObject) string {
	if dir := CurrentDirectory(meta); dir != "" {
		return dir
	}
	return S(meta["cwd"])
}
