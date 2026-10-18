//go:build !darwin

package update

import (
	"context"
	"fmt"
)

func extractMac(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("macOS app updates require macOS")
}
