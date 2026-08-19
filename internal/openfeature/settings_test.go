// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/config"
)

func TestResolveSource(t *testing.T) {
	for name, tt := range map[string]struct {
		in                sourceInputs
		wantSource        Source
		wantLegacyDecided bool
	}{
		"nothing set defaults to agentless": {
			in:         sourceInputs{},
			wantSource: SourceAgentless,
		},
		"kill switch wins over everything": {
			in: sourceInputs{
				enabledSet: true, enabled: false,
				sourceSet: true, source: "remote_config",
				legacyEnabledSet: true, legacyEnabled: true,
			},
			wantSource: SourceDisabled,
		},
		"source agentless, case-insensitive and trimmed": {
			in:         sourceInputs{sourceSet: true, source: "  AGENTLESS  "},
			wantSource: SourceAgentless,
		},
		"source remote_config, case-insensitive": {
			in:         sourceInputs{sourceSet: true, source: "Remote_Config"},
			wantSource: SourceRemoteConfig,
		},
		"source offline": {
			in:         sourceInputs{sourceSet: true, source: "OFFLINE"},
			wantSource: SourceDisabled,
		},
		"unrecognized source fails closed": {
			in:         sourceInputs{sourceSet: true, source: "bogus"},
			wantSource: SourceDisabled,
		},
		"source set but blank falls through to later rules": {
			in:         sourceInputs{sourceSet: true, source: "   ", enabledSet: true, enabled: true},
			wantSource: SourceAgentless,
		},
		"explicit enabled=true implies agentless, not remote_config": {
			in:         sourceInputs{enabledSet: true, enabled: true},
			wantSource: SourceAgentless,
		},
		"legacy key true grandfathers remote_config": {
			in:                sourceInputs{legacyEnabledSet: true, legacyEnabled: true},
			wantSource:        SourceRemoteConfig,
			wantLegacyDecided: true,
		},
		"legacy key false disables": {
			in:                sourceInputs{legacyEnabledSet: true, legacyEnabled: false},
			wantSource:        SourceDisabled,
			wantLegacyDecided: true,
		},
		"explicit source takes precedence over legacy key": {
			in: sourceInputs{
				sourceSet: true, source: "agentless",
				legacyEnabledSet: true, legacyEnabled: false,
			},
			wantSource: SourceAgentless,
		},
		"explicit enabled=true takes precedence over legacy key": {
			in: sourceInputs{
				enabledSet: true, enabled: true,
				legacyEnabledSet: true, legacyEnabled: false,
			},
			wantSource: SourceAgentless,
		},
	} {
		t.Run(name, func(t *testing.T) {
			source, legacyDecided := resolveSource(tt.in)
			assert.Equal(t, tt.wantSource, source)
			assert.Equal(t, tt.wantLegacyDecided, legacyDecided)
		})
	}
}

func TestResolveSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg := config.CreateNew()
		settings := ResolveSettings(cfg)
		assert.Equal(t, SourceAgentless, settings.Source)
		assert.False(t, settings.LegacyKeyDecided)
	})

	t.Run("legacy key grandfathers remote_config", func(t *testing.T) {
		t.Setenv("DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED", "true")
		cfg := config.CreateNew()
		settings := ResolveSettings(cfg)
		assert.Equal(t, SourceRemoteConfig, settings.Source)
		assert.True(t, settings.LegacyKeyDecided)
	})
}

func TestRemoteConfigSourceSelected(t *testing.T) {
	t.Run("false by default", func(t *testing.T) {
		cfg := config.CreateNew()
		assert.False(t, RemoteConfigSourceSelected(cfg))
	})

	t.Run("true when legacy key opts in", func(t *testing.T) {
		t.Setenv("DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED", "true")
		cfg := config.CreateNew()
		assert.True(t, RemoteConfigSourceSelected(cfg))
	})

	t.Run("false when explicit source is agentless", func(t *testing.T) {
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "agentless")
		cfg := config.CreateNew()
		assert.False(t, RemoteConfigSourceSelected(cfg))
	})
}
