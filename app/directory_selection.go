package main

import (
	"errors"
	"os"
)

// Native folder dialogs can be canceled. A nonempty result must still be a
// directory before it can become a project folder or a connector root.
func selectedDirectory(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("selected path is not a directory")
	}
	return path, nil
}
