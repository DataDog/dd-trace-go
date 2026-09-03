// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var claudeForwardedEnv = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_CUSTOM_HEADERS",
}

type claudeManagedSettings struct {
	APIKeyHelper string         `json:"apiKeyHelper"`
	Env          map[string]any `json:"env"`
}

func claudeContainerAuth(ctx context.Context) (map[string]string, error) {
	env := make(map[string]string)
	for _, key := range claudeForwardedEnv {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}
	if env["ANTHROPIC_API_KEY"] != "" || env["ANTHROPIC_AUTH_TOKEN"] != "" {
		return env, nil
	}

	settings, err := readClaudeManagedSettings()
	if err != nil {
		return nil, err
	}
	if settings.APIKeyHelper == "" {
		return nil, fmt.Errorf("Claude container authentication requires ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, or a managed apiKeyHelper")
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", settings.APIKeyHelper)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run Claude apiKeyHelper: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return nil, fmt.Errorf("Claude apiKeyHelper returned an empty token")
	}
	env["ANTHROPIC_AUTH_TOKEN"] = token
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_CUSTOM_HEADERS"} {
		if value, ok := settings.Env[key].(string); ok && value != "" {
			env[key] = value
		}
	}
	return env, nil
}

func readClaudeManagedSettings() (*claudeManagedSettings, error) {
	paths := []string{
		"/Library/Application Support/ClaudeCode/managed-settings.json",
		"/etc/claude-code/managed-settings.json",
	}
	if path := os.Getenv("CLAUDE_MANAGED_SETTINGS"); path != "" {
		paths = append([]string{path}, paths...)
	}
	for _, path := range paths {
		body, err := os.ReadFile(filepath.Clean(path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read Claude managed settings: %w", err)
		}
		var settings claudeManagedSettings
		if err := json.Unmarshal(body, &settings); err != nil {
			return nil, fmt.Errorf("decode Claude managed settings: %w", err)
		}
		return &settings, nil
	}
	return &claudeManagedSettings{}, nil
}
