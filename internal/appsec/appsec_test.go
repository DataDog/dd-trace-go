// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatus(t *testing.T) {
	enabled, err := isEnabled()
	require.NoError(t, err)
	status := Status()
	if enabled {
		require.Equal(t, "enabled", status)
	} else {
		require.Equal(t, "disabled", status)
	}
}
