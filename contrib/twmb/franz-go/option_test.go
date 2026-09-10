// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kgo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDataStreamsSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		h := newTracingHook()
		assert.False(t, h.cfg.dataStreamsEnabled)
	})
	t.Run("withOption", func(t *testing.T) {
		h := newTracingHook(WithDataStreams())
		assert.True(t, h.cfg.dataStreamsEnabled)
	})
	t.Run("withEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
		h := newTracingHook()
		assert.True(t, h.cfg.dataStreamsEnabled)
	})
	t.Run("optionOverridesEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "false")
		h := newTracingHook()
		WithDataStreams().apply(&h.cfg)
		assert.True(t, h.cfg.dataStreamsEnabled)
	})
}
