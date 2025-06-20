// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package declarativeconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeclarativeConfigSource(t *testing.T) {
	data := `
file_format: "0.4"
disabled: false
log_level: debug`

	dc, err := validateConfig([]byte(data))
	assert.NoError(t, err)
	require.NotNil(t, dc)

	// Type assert the values from the map
	fileFormat, ok := dc["file_format"].(string)
	assert.True(t, ok, "file_format should be a string")
	assert.Equal(t, "0.4", fileFormat)

	disabled, ok := dc["disabled"].(bool)
	assert.True(t, ok, "disabled should be a bool")
	assert.Equal(t, false, disabled)

	logLevel, ok := dc["log_level"].(string)
	assert.True(t, ok, "log_level should be a string")
	assert.Equal(t, "debug", logLevel)
}
