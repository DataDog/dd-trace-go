// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Build when CGO is disabled or the target OS or Arch are not supported
//go:build !appsec || !cgo || windows || !amd64
// +build !appsec !cgo windows !amd64

package waf

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHealth(t *testing.T) {
	version, err := Health()
	require.Error(t, err)
	require.Nil(t, version)
}
