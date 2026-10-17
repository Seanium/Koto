package main

import (
	"encoding/json"
	"fmt"
)

func contextSessionID(data string) (string, error) {
	var id string
	if err := json.Unmarshal([]byte(data), &id); err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("empty context menu session ID")
	}
	return id, nil
}
