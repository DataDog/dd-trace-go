// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeContainerAuthUsesManagedHelperAsAuthToken(t *testing.T) {
	for _, key := range claudeForwardedEnv {
		t.Setenv(key, "")
	}
	settings := filepath.Join(t.TempDir(), "managed-settings.json")
	if err := os.WriteFile(settings, []byte(`{
		"apiKeyHelper": "printf managed-token",
		"env": {
			"ANTHROPIC_BASE_URL": "https://gateway.example",
			"ANTHROPIC_CUSTOM_HEADERS": "X-Test: value"
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_MANAGED_SETTINGS", settings)

	env, err := claudeContainerAuth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := env["ANTHROPIC_AUTH_TOKEN"]; got != "managed-token" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q, want managed-token", got)
	}
	if got := env["ANTHROPIC_API_KEY"]; got != "" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want empty", got)
	}
	if got := env["ANTHROPIC_BASE_URL"]; got != "https://gateway.example" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want gateway URL", got)
	}
	if got := env["ANTHROPIC_CUSTOM_HEADERS"]; got != "X-Test: value" {
		t.Fatalf("ANTHROPIC_CUSTOM_HEADERS = %q, want custom headers", got)
	}
}
