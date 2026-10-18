package zcode

import (
	"encoding/json"
	"fmt"
	"koto/internal/core"
	"os"
	"path/filepath"
	"strings"
)

// New copied sessions use the target's explicit configured default. Resuming a
// persisted ZCode session deliberately does not fall back to the global default;
// its runtime/model_selection entry is required. Source model IDs are history.
func (c *connector) targetModel() (core.JsonObject, error) {
	b, err := os.ReadFile(filepath.Join(c.root, "v2", "provider_config.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var config struct {
		Version int `json:"schemaVersion"`
		Config  struct {
			Default json.RawMessage `json:"defaultModelSelection"`
		} `json:"config"`
	}
	if err = json.Unmarshal(b, &config); err != nil {
		return nil, fmt.Errorf("invalid ZCode target provider configuration")
	}
	if config.Version != 1 {
		return nil, fmt.Errorf("unsupported ZCode provider config schema %d", config.Version)
	}
	if len(config.Config.Default) == 0 || string(config.Config.Default) == "null" {
		return nil, nil
	}
	var selection core.JsonObject
	if err = json.Unmarshal(config.Config.Default, &selection); err != nil {
		return nil, fmt.Errorf("invalid ZCode default model selection")
	}
	if strings.TrimSpace(core.S(selection["providerId"])) == "" || strings.TrimSpace(core.S(selection["modelId"])) == "" {
		return nil, fmt.Errorf("ZCode default model has no provider/model identity")
	}
	for k := range selection {
		if k != "providerId" && k != "modelId" && k != "options" {
			return nil, fmt.Errorf("unsupported ZCode model selection field %q", k)
		}
	}
	if value, present := selection["options"]; present && value != nil {
		if _, ok := value.(map[string]any); !ok {
			return nil, fmt.Errorf("invalid ZCode model options")
		}
	}
	for k := range core.Obj(selection["options"]) {
		if k != "reasoningLevel" {
			return nil, fmt.Errorf("unsupported ZCode model option %q", k)
		}
	}
	if value, present := core.Obj(selection["options"])["reasoningLevel"]; present {
		if _, ok := value.(string); !ok {
			return nil, fmt.Errorf("invalid ZCode reasoning option")
		}
	}
	return selection, nil
}
