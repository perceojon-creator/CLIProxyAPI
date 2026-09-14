package config

import (
	"os"
	"strings"
)

// applyEnvironmentAPIKeys inspects environment variables (such as CLI_PROXY_API_KEY,
// PROXY_API_KEY, or API_KEY) loaded from .env or system environment and prepends
// them to the active API keys list, eliminating the need to commit plain-text
// credentials to config.yaml.
func (c *Config) applyEnvironmentAPIKeys() {
	if c == nil {
		return
	}
	envCandidates := []string{
		"CLI_PROXY_API_KEY",
		"PROXY_API_KEY",
		"CLI_PROXY_API_KEYS",
		"PROXY_API_KEYS",
		"API_KEY",
		"API_KEYS",
	}

	var envKeys []string
	seen := make(map[string]struct{})

	for _, k := range c.APIKeys {
		trimmed := strings.TrimSpace(k)
		if trimmed != "" {
			seen[trimmed] = struct{}{}
		}
	}

	for _, envVar := range envCandidates {
		val := strings.TrimSpace(os.Getenv(envVar))
		if val == "" {
			continue
		}
		// Support comma-separated or newline-separated keys
		parts := strings.FieldsFunc(val, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r'
		})
		for _, part := range parts {
			key := strings.TrimSpace(part)
			if key == "" {
				continue
			}
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				envKeys = append(envKeys, key)
			}
		}
	}

	if len(envKeys) > 0 {
		c.APIKeys = append(envKeys, c.APIKeys...)
	}

	// Resolve environment variables for upstream OpenAI compatibility providers
	// If an api-key starts with "$", or is empty, resolve it from os.Getenv.
	for i := range c.OpenAICompatibility {
		compat := &c.OpenAICompatibility[i]
		normName := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(compat.Name), "-", "_"))
		for j := range compat.APIKeyEntries {
			entry := &compat.APIKeyEntries[j]
			val := strings.TrimSpace(entry.APIKey)
			if strings.HasPrefix(val, "$") {
				envName := strings.TrimPrefix(val, "$")
				if resolved := strings.TrimSpace(os.Getenv(envName)); resolved != "" {
					entry.APIKey = resolved
				}
			} else if val == "" && normName != "" {
				// Try provider-specific env var like UNOROUTER_API_KEY or B_AI_FREE_API_KEY
				if resolved := strings.TrimSpace(os.Getenv(normName + "_API_KEY")); resolved != "" {
					entry.APIKey = resolved
				}
			}
		}
	}
}
