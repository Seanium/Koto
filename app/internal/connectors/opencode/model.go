package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"koto/internal/core"
)

func (c *connector) nativeEnv() []string {
	env := os.Environ()
	set := func(k, v string) {
		for i := len(env) - 1; i >= 0; i-- {
			if strings.HasPrefix(env[i], k+"=") {
				env = append(env[:i], env[i+1:]...)
			}
		}
		env = append(env, k+"="+v)
	}
	set("OPENCODE_DB", c.db)
	if c.isolated {
		set("XDG_DATA_HOME", filepath.Join(c.root, "runtime", "data"))
		set("XDG_CACHE_HOME", filepath.Join(c.root, "runtime", "cache"))
		set("XDG_STATE_HOME", filepath.Join(c.root, "runtime", "state"))
	}
	return env
}

// OpenCode inherits the last user's model on resume. A session-level native
// default keeps foreign provider names in history from selecting an invalid model.
// Only the resolved model is retained, never the config (which may contain keys).
func (c *connector) targetModel(cwd string) (core.JsonObject, error) {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	if c.modelFixture != nil {
		return c.modelFixture, nil
	}
	if cwd == c.modelCwd && time.Now().Before(c.modelUntil) {
		return c.model, nil
	}
	// OpenCode can initialize its isolated runtime on the first invocation.
	// A cold native Windows launch can take over 20 seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd, err := nativeCommand(ctx, "debug", "config", "--pure")
	if err != nil {
		return nil, err
	}
	cmd.Dir = cwd
	cmd.Env = c.nativeEnv()
	b, err := readModelConfiguration(ctx, cmd)
	if err != nil {
		return nil, err
	}
	var config struct {
		Model string `json:"model"`
	}
	if err = json.Unmarshal(b, &config); err != nil {
		return nil, fmt.Errorf("OpenCode did not return valid resolved configuration")
	}
	provider, model, ok := strings.Cut(config.Model, "/")
	if !ok || provider == "" || model == "" {
		return nil, fmt.Errorf("set a default model in OpenCode (opencode.json model: provider/model) before copying a conversation")
	}
	c.model = core.JsonObject{"providerID": provider, "id": model}
	c.modelCwd = cwd
	c.modelUntil = time.Now().Add(30 * time.Second)
	return c.model, nil
}

func readModelConfiguration(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	b, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("read OpenCode model configuration timed out or was canceled: %w", ctx.Err())
		}
		// Resolved config and stderr may contain provider credentials.
		return nil, fmt.Errorf("read OpenCode model configuration: %w", err)
	}
	return b, nil
}
