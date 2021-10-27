// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !appsec
// +build !appsec

package appsec_test

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/stretchr/testify/require"
)

func TestStatus(t *testing.T) {
	require.Equal(t, "disabled", appsec.Status())
}
