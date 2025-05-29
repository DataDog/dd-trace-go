// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package processtags

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestProcessTags(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		t.Setenv("DD_EXPERIMENTAL_COLLECT_PROCESS_TAGS_ENABLED", "true")
		ResetConfig()

		p := Get()
		assert.NotNil(t, p)
		assert.NotEmpty(t, p.String())
		assert.NotEmpty(t, p.Slice())
	})

	t.Run("disabled", func(t *testing.T) {
		t.Setenv("DD_EXPERIMENTAL_COLLECT_PROCESS_TAGS_ENABLED", "false")
		ResetConfig()

		p := Get()
		assert.Nil(t, p)
		assert.Empty(t, p.String())
		assert.Empty(t, p.Slice())
	})
}
