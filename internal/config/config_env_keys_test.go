package config

import (
	"os"
	"testing"
)

func TestApplyEnvironmentAPIKeys(t *testing.T) {
	os.Setenv("CLI_PROXY_API_KEY", "sk-env-key-1, sk-env-key-2")
	defer os.Unsetenv("CLI_PROXY_API_KEY")

	cfg, err := LoadConfigOptional("../../config.yaml", true)
	if err != nil {
		t.Fatalf("LoadConfigOptional failed: %v", err)
	}

	if len(cfg.APIKeys) < 2 {
		t.Fatalf("expected at least 2 keys from env, got: %v", cfg.APIKeys)
	}
	if cfg.APIKeys[0] != "sk-env-key-1" || cfg.APIKeys[1] != "sk-env-key-2" {
		t.Errorf("unexpected key order: %v", cfg.APIKeys)
	}
}
