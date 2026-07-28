// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"strings"
	"testing"
)

func TestAgentNameWireSafe(t *testing.T) {
	t.Run("empty-name-is-safe", func(t *testing.T) {
		if !AgentNameWireSafe("") {
			t.Error("empty name should be wire-safe")
		}
	})
	t.Run("typical-name-is-safe", func(t *testing.T) {
		if !AgentNameWireSafe("my_agent") {
			t.Error("typical agent name should be wire-safe")
		}
	})
	t.Run("equals-sign-is-safe", func(t *testing.T) {
		if !AgentNameWireSafe("agent=v2") {
			t.Error("name with equals sign should be wire-safe (only illegal in keys)")
		}
	})
	t.Run("comma-is-unsafe", func(t *testing.T) {
		if AgentNameWireSafe("agent,bad") {
			t.Error("name with comma should not be wire-safe")
		}
	})
	t.Run("control-char-is-unsafe", func(t *testing.T) {
		if AgentNameWireSafe("agent\x00name") {
			t.Error("name with control character should not be wire-safe")
		}
	})
	t.Run("non-ascii-is-unsafe", func(t *testing.T) {
		if AgentNameWireSafe("agënt") {
			t.Error("name with non-ASCII byte should not be wire-safe")
		}
	})
	t.Run("256-byte-name-is-safe", func(t *testing.T) {
		name := strings.Repeat("a", 256)
		if !AgentNameWireSafe(name) {
			t.Error("256-byte name should be wire-safe")
		}
	})
	t.Run("257-byte-name-is-unsafe", func(t *testing.T) {
		name := strings.Repeat("a", 257)
		if AgentNameWireSafe(name) {
			t.Error("257-byte name should not be wire-safe")
		}
	})
}
