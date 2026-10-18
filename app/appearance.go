package main

func nativeWindowDark(preference string, systemDark, highContrast bool) bool {
	if highContrast {
		return false
	}
	switch preference {
	case "dark":
		return true
	case "light":
		return false
	default:
		return systemDark
	}
}
