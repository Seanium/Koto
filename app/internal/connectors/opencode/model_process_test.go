package opencode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestModelConfigurationProcessHelper(t *testing.T) {
	switch os.Getenv("KOTO_MODEL_PROCESS_HELPER") {
	case "wait":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "private-provider-secret")
		os.Exit(1)
	case "ok":
		fmt.Fprintln(os.Stdout, `{"model":"fixture/model"}`)
		os.Exit(0)
	}
}

func TestModelConfigurationProcessErrors(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"ok", "fail", "wait"} {
		t.Run(mode, func(t *testing.T) {
			budget := 10 * time.Second
			if mode == "wait" {
				budget = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, "-test.run=^TestModelConfigurationProcessHelper$")
			cmd.Env = append(os.Environ(), "KOTO_MODEL_PROCESS_HELPER="+mode)
			b, err := readModelConfiguration(ctx, cmd)
			switch mode {
			case "ok":
				if err != nil || !strings.Contains(string(b), `"fixture/model"`) {
					t.Fatalf("read configuration: %s, %v", b, err)
				}
			case "fail":
				if err == nil || errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "private-provider-secret") {
					t.Fatalf("configuration error leaked or misclassified: %v", err)
				}
			case "wait":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("deadline hidden by process exit: %v", err)
				}
			}
		})
	}
}
