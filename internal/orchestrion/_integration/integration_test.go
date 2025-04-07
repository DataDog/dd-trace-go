// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integration_test

import (
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"
)

//go:generate go run github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/generator

func TestOrchestrionPresent(t *testing.T) {
	require.True(t, built.WithOrchestrion, "this test was not built with orchestrion enabled")
}
